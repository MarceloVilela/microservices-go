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
)

// Spec descreve um serviço.
type Spec struct {
	Name        string
	DefaultAddr string
	// Require é o que o serviço exige do broker (verificação passiva no /readyz).
	Require amqpx.Requirement
	// Verifier substitui a verificação padrão (usado em testes).
	Verifier func(ctx context.Context, amqpURL string) error
}

// Vars é a lista completa de variáveis de ambiente de um serviço.
func Vars(defaultAddr string) []config.Var {
	return []config.Var{
		{Name: "LOG_LEVEL", Default: "info", Validate: config.OneOf("debug", "info", "warn", "error")},
		{Name: "HTTP_ADDR", Default: defaultAddr, Validate: validAddr},
		{Name: "AMQP_URL", Required: true, Secret: true, Validate: amqpx.ValidURL},
		{Name: "READY_TIMEOUT", Default: "2s", Validate: config.ValidDuration},
		// por quanto tempo o /readyz reaproveita o último resultado (evita uma conexão AMQP por probe)
		{Name: "READY_CACHE_TTL", Default: "2s", Validate: config.ValidDuration},
	}
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
	vars := Vars(spec.DefaultAddr)
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
	vals, err := config.Load(environ, Vars(spec.DefaultAddr))
	if err != nil {
		say(stderr, "configuração inválida:\n%v\n", err)
		return 1
	}
	log := logging.New(stderr, spec.Name, vals.String("LOG_LEVEL"))
	log.Info("configuração carregada", "variaveis", config.Names(Vars(spec.DefaultAddr)))

	amqpURL := vals.String("AMQP_URL")
	verify := spec.Verifier
	if verify == nil {
		verify = func(ctx context.Context, url string) error { return amqpx.Verify(ctx, url, spec.Require) }
	}
	check := func(ctx context.Context) error {
		err := verify(ctx, amqpURL)
		if err != nil {
			log.Warn("readyz: topologia ou broker indisponível", "erro", err)
		}
		return err
	}

	srv := &http.Server{
		Addr:              vals.String("HTTP_ADDR"),
		Handler:           health.Handler(cached(check, vals.Duration("READY_CACHE_TTL")), vals.Duration("READY_TIMEOUT")),
		ReadHeaderTimeout: 5 * time.Second,
		ErrorLog:          slog.NewLogLogger(log.Handler(), slog.LevelWarn),
	}
	errc := make(chan error, 1)
	go func() { errc <- srv.ListenAndServe() }()
	log.Info("servindo", "addr", srv.Addr)

	select {
	case err := <-errc:
		log.Error("servidor parou", "erro", err)
		return 1
	case <-ctx.Done():
	}
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := srv.Shutdown(shutdownCtx); err != nil && !errors.Is(err, http.ErrServerClosed) {
		log.Error("shutdown", "erro", err)
		return 1
	}
	log.Info("encerrado")
	return 0
}
