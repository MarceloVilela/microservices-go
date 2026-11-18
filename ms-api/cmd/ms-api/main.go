// Comando ms-api: leitura (lotes, fotos, miniaturas, DLQ), retry da DLQ e SSE (Fase 6).
package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/MarceloVilela/microservices-go/ms-api/internal/adapters/httpapi"
	"github.com/MarceloVilela/microservices-go/ms-api/internal/adapters/postgres"
	"github.com/MarceloVilela/microservices-go/ms-api/internal/adapters/sse"
	"github.com/MarceloVilela/microservices-go/ms-api/internal/adapters/thumbs"
	"github.com/MarceloVilela/microservices-go/platform/amqpx"
	"github.com/MarceloVilela/microservices-go/platform/config"
	"github.com/MarceloVilela/microservices-go/platform/db"
	"github.com/MarceloVilela/microservices-go/platform/httpx"
	"github.com/MarceloVilela/microservices-go/platform/service"
	"github.com/MarceloVilela/microservices-go/platform/topology"
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	os.Exit(service.Run(ctx, spec(), os.Args[1:], os.Environ(), os.Stdout, os.Stderr))
}

func spec() service.Spec {
	return service.Spec{
		Name:        "ms-api",
		DefaultAddr: ":8082",
		Require:     amqpx.Requirement{Queues: []topology.Queue{topology.PhotoDLQ}},
		ExtraVars: []config.Var{
			{Name: "DATABASE_URL", Required: true, Secret: true, Validate: db.ValidURL},
			// obrigatório: o ms-api expõe leitura e reprocessamento (token de demonstração, não é segurança real)
			{Name: "DEMO_TOKEN", Required: true, Secret: true},
			{Name: "CORS_ORIGINS"},
			{Name: "THUMBS_DIR", Default: "/data/thumbs"},
			{Name: "SSE_HEARTBEAT", Default: "15s", Validate: config.ValidDuration},
			{Name: "SSE_WRITE_TIMEOUT", Default: "5s", Validate: config.ValidDuration},
			{Name: "EVENTS_RETENTION", Default: "24h", Validate: config.ValidDuration},
		},
		Setup: setup,
	}
}

func setup(ctx context.Context, vals config.Values, log *slog.Logger) (*service.Runtime, error) {
	pool, err := db.Open(ctx, vals.String("DATABASE_URL"))
	if err != nil {
		return nil, err
	}
	// o ms-api não é dono do esquema: só verifica no /readyz (o ms-ingest migra; ADR 0007)
	store := postgres.Store{Pool: pool}
	files := thumbs.Store{Dir: vals.String("THUMBS_DIR")}
	origins := origins(vals.String("CORS_ORIGINS"))
	mws := []httpx.Middleware{httpx.OriginGuard(origins), httpx.CORS(origins), httpx.Token(vals.String("DEMO_TOKEN"))}

	bc := &sse.Broadcaster{}
	hub := &sse.Hub{URL: vals.String("DATABASE_URL"), Broadcaster: bc, Log: log}
	events := &sse.Handler{
		Source: store, Waker: bc, Shutdown: ctx, Log: log,
		Heartbeat: vals.Duration("SSE_HEARTBEAT"), Poll: 10 * time.Second, // só rede de segurança: o LISTEN acorda os streams
		WriteTimeout: vals.Duration("SSE_WRITE_TIMEOUT"), GapWait: 2 * time.Second, MaxPerRead: 200,
	}
	api := httpapi.Handler{Reader: store, Thumbs: files, Log: log}
	retention := vals.Duration("EVENTS_RETENTION")

	return &service.Runtime{
		Mount: func(mux *http.ServeMux) {
			api.Mount(mux, mws...)
			mux.Handle(sse.Route, httpx.Chain(events, mws...))
			// preflight: OriginGuard e CORS respondem; o Token deixa OPTIONS passar
			mux.Handle("OPTIONS /", httpx.Chain(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				httpx.Problem(w, http.StatusMethodNotAllowed, "method_not_allowed", "método não permitido")
			}), mws...))
			// consulta o banco e expõe estado interno: mesmos middlewares das demais rotas
			mux.Handle("GET /statusz", httpx.Chain(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				_, hi, _ := store.Bounds(r.Context())
				w.Header().Set("Content-Type", "application/json")
				_, _ = fmt.Fprintf(w, `{"sse_clients":%d,"last_seq":%d}`+"\n", events.Clients(), hi)
			}), mws...))
		},
		Ready: func(ctx context.Context) error {
			if err := pool.Ping(ctx); err != nil {
				return err
			}
			if err := db.Verify(ctx, pool); err != nil {
				return err
			}
			return files.Check()
		},
		Background: []func(context.Context) error{
			hub.Run,
			func(ctx context.Context) error { return prune(ctx, store, retention, log) },
		},
		Close: pool.Close,
	}, nil
}

// prune apaga eventos fora da retenção a cada hora (e uma vez na partida). Quem voltar depois da
// janela recebe `event: reset` no SSE (ADR 0016). Falha da poda não derruba o serviço.
func prune(ctx context.Context, s postgres.Store, retention time.Duration, log *slog.Logger) error {
	tick := time.NewTicker(time.Hour)
	defer tick.Stop()
	for {
		n, err := s.PruneEvents(ctx, retention)
		switch {
		case ctx.Err() != nil:
			return ctx.Err()
		case err != nil && !errors.Is(err, context.Canceled):
			log.Warn("podar eventos", "erro", err)
		case n > 0:
			log.Info("eventos podados", "n", n, "retencao", retention)
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-tick.C:
		}
	}
}

// origins só aceita origens exatas (sem barra final); o CORS compara por igualdade.
func origins(s string) []string {
	var out []string
	for _, o := range strings.Split(s, ",") {
		if o = strings.TrimSpace(o); o != "" {
			out = append(out, o)
		}
	}
	return out
}
