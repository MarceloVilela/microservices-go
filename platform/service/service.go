// Package service é o bootstrap comum dos serviços: subcomandos serve,
// healthcheck e config check, logger, servidor HTTP e shutdown gracioso.
package service

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"strconv"
	"sync"
	"time"

	"github.com/MarceloVilela/microservices-go/platform/amqpx"
	"github.com/MarceloVilela/microservices-go/platform/config"
	"github.com/MarceloVilela/microservices-go/platform/health"
	"github.com/MarceloVilela/microservices-go/platform/logging"
	"github.com/MarceloVilela/microservices-go/platform/metrics"
)

// Spec descreve um serviço.
type Spec struct {
	Name        string
	DefaultAddr string
	// Require é o que o serviço exige do broker (verificação passiva no /readyz).
	Require amqpx.Requirement
	// ExtraVars são as variáveis do serviço além das comuns (validadas no boot e no config check).
	ExtraVars []config.Var
	// Setup monta as dependências do serviço (banco, rotas, tarefas). Opcional.
	Setup func(ctx context.Context, vals config.Values, log *slog.Logger) (*Runtime, error)
	// Metrics, se presente, expõe GET /metrics (sem Bearer, como /healthz) e mede cada
	// requisição HTTP. O Setup do serviço registra aqui as métricas próprias.
	Metrics *metrics.Registry
	// Verifier substitui a verificação padrão (usado em testes).
	Verifier func(ctx context.Context, amqpURL string) error
}

// Runtime é o que o Setup devolve.
type Runtime struct {
	// Mount registra as rotas do serviço no mux (além de /healthz e /readyz).
	Mount func(mux *http.ServeMux)
	// Ready é uma checagem de prontidão extra (ex.: ping no banco), somada à do broker.
	Ready health.Check
	// Background são tarefas de longa duração (ex.: relay). Rodam até ctx terminar;
	// uma que devolve erro diferente de context.Canceled derruba o serviço.
	Background []func(ctx context.Context) error
	// Close libera recursos depois do shutdown do servidor e das tarefas.
	Close func()
}

// Vars é a lista completa de variáveis de ambiente de um serviço.
func Vars(spec Spec) []config.Var {
	return append([]config.Var{
		{Name: "LOG_LEVEL", Default: "info", Validate: config.OneOf("debug", "info", "warn", "error")},
		{Name: "HTTP_ADDR", Default: spec.DefaultAddr, Validate: validAddr},
		{Name: "AMQP_URL", Required: true, Secret: true, Validate: amqpx.ValidURL},
		{Name: "READY_TIMEOUT", Default: "2s", Validate: config.ValidDuration},
		// por quanto tempo o /readyz reaproveita o último resultado (evita uma conexão AMQP por probe)
		{Name: "READY_CACHE_TTL", Default: "2s", Validate: config.ValidDuration},
	}, spec.ExtraVars...)
}

// validAddr aceita host:porta com porta numérica de 0 a 65535 (host pode ser vazio).
func validAddr(s string) error {
	_, port, err := net.SplitHostPort(s)
	if err != nil {
		return errors.New("esperado host:porta (ex.: :8081)")
	}
	n, err := strconv.Atoi(port)
	if err != nil || n < 0 || n > 65535 {
		return errors.New("porta inválida")
	}
	return nil
}

// probeHost é o host que o healthcheck disca: o configurado, ou loopback quando
// o servidor escuta em todas as interfaces.
func probeHost(host string) string {
	if ip := net.ParseIP(host); host == "" || (ip != nil && ip.IsUnspecified()) {
		return "127.0.0.1"
	}
	return host
}

// cached reaproveita o resultado de check por ttl. Chamadas concorrentes
// esperam a verificação em andamento em vez de abrir outra conexão.
func cached(check health.Check, ttl time.Duration) health.Check {
	var (
		mu   sync.Mutex
		at   time.Time
		last error
	)
	return func(ctx context.Context) error {
		mu.Lock()
		defer mu.Unlock()
		if !at.IsZero() && time.Since(at) < ttl {
			return last
		}
		last = check(ctx)
		at = time.Now()
		return last
	}
}

// say escreve em stdout/stderr; falha de escrita na saída padrão não tem tratamento útil.
func say(w io.Writer, format string, args ...any) { _, _ = fmt.Fprintf(w, format, args...) }

// Run executa o serviço e devolve o código de saída: 0 ok, 1 falha, 2 uso errado.
func Run(ctx context.Context, spec Spec, args []string, environ []string, stdout, stderr io.Writer) int {
	cmd := "serve"
	if len(args) > 0 {
		cmd = args[0]
	}
	switch {
	case cmd == "serve" && len(args) <= 1:
		return serve(ctx, spec, environ, stderr)
	case cmd == "healthcheck" && len(args) == 1:
		return healthcheck(ctx, spec, environ, stderr)
	case cmd == "config" && len(args) == 2 && args[1] == "check":
		return configCheck(spec, environ, stdout, stderr)
	default:
		say(stderr, "uso: %s [serve | healthcheck | config check]\n", spec.Name)
		return 2
	}
}

func configCheck(spec Spec, environ []string, stdout, stderr io.Writer) int {
	vars := Vars(spec)
	if _, err := config.Load(environ, vars); err != nil {
		say(stderr, "configuração inválida:\n%v\n", err)
		return 1
	}
	say(stdout, "configuração ok; variáveis:\n")
	for _, n := range config.Names(vars) {
		say(stdout, "  %s\n", n)
	}
	return 0
}

func healthcheck(ctx context.Context, spec Spec, environ []string, stderr io.Writer) int {
	// só HTTP_ADDR importa aqui; o healthcheck não exige AMQP_URL
	vals, err := config.Load(environ, []config.Var{{Name: "HTTP_ADDR", Default: spec.DefaultAddr}})
	if err != nil {
		say(stderr, "%v\n", err)
		return 1
	}
	host, port, err := net.SplitHostPort(vals.String("HTTP_ADDR"))
	if err != nil {
		say(stderr, "HTTP_ADDR inválida\n")
		return 1
	}
	ctx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "http://"+net.JoinHostPort(probeHost(host), port)+"/healthz", nil)
	if err != nil {
		say(stderr, "%v\n", err)
		return 1
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		say(stderr, "healthcheck: %v\n", err)
		return 1
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		say(stderr, "healthcheck: status %d\n", resp.StatusCode)
		return 1
	}
	return 0
}

func serve(ctx context.Context, spec Spec, environ []string, stderr io.Writer) int {
	vals, err := config.Load(environ, Vars(spec))
	if err != nil {
		say(stderr, "configuração inválida:\n%v\n", err)
		return 1
	}
	log := logging.New(stderr, spec.Name, vals.String("LOG_LEVEL"))
	log.Info("configuração carregada", "variaveis", config.Names(Vars(spec)))

	runCtx, cancel := context.WithCancel(ctx)
	defer cancel()

	var rt Runtime
	if spec.Setup != nil {
		r, err := spec.Setup(runCtx, vals, log)
		if err != nil {
			log.Error("montar o serviço", "erro", err)
			return 1
		}
		rt = *r
	}
	if rt.Close != nil {
		defer rt.Close()
	}

	amqpURL := vals.String("AMQP_URL")
	verify := spec.Verifier
	if verify == nil {
		verify = func(ctx context.Context, url string) error { return amqpx.Verify(ctx, url, spec.Require) }
	}
	check := func(ctx context.Context) error {
		var errs []error
		if err := verify(ctx, amqpURL); err != nil {
			log.Warn("readyz: topologia ou broker indisponível", "erro", err)
			errs = append(errs, err)
		}
		if rt.Ready != nil {
			if err := rt.Ready(ctx); err != nil {
				log.Warn("readyz: dependência indisponível", "erro", err)
				errs = append(errs, err)
			}
		}
		return errors.Join(errs...)
	}

	mux := health.Handler(cached(check, vals.Duration("READY_CACHE_TTL")), vals.Duration("READY_TIMEOUT"))
	if rt.Mount != nil {
		rt.Mount(mux)
	}
	var handler http.Handler = mux
	if spec.Metrics != nil {
		mux.Handle("GET /metrics", spec.Metrics.Handler())
		handler = spec.Metrics.Middleware(mux)
	}
	srv := &http.Server{
		Addr:              vals.String("HTTP_ADDR"),
		Handler:           handler,
		ReadHeaderTimeout: 5 * time.Second,
		// o corpo do POST /batches tem no máximo 1 MiB; clientes lentos não podem prender conexões
		ReadTimeout:  15 * time.Second,
		WriteTimeout: 30 * time.Second,
		IdleTimeout:  60 * time.Second,
		ErrorLog:     slog.NewLogLogger(log.Handler(), slog.LevelWarn),
	}
	errc := make(chan error, 1+len(rt.Background))
	go func() { errc <- srv.ListenAndServe() }()
	var bg sync.WaitGroup
	for _, task := range rt.Background {
		bg.Add(1)
		go func() {
			defer bg.Done()
			if err := task(runCtx); err != nil && !errors.Is(err, context.Canceled) {
				errc <- err
			}
		}()
	}
	log.Info("servindo", "addr", srv.Addr)

	code := 0
	select {
	case err := <-errc:
		log.Error("serviço parou", "erro", err)
		code = 1
	case <-ctx.Done():
	}
	cancel() // para as tarefas em segundo plano
	shutdownCtx, stopShutdown := context.WithTimeout(context.Background(), 10*time.Second)
	defer stopShutdown()
	if err := srv.Shutdown(shutdownCtx); err != nil && !errors.Is(err, http.ErrServerClosed) {
		log.Error("shutdown", "erro", err)
		code = 1
	}
	bg.Wait()
	log.Info("encerrado")
	return code
}
