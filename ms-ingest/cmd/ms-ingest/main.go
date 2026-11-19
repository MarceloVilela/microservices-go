// Comando ms-ingest.
package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/MarceloVilela/microservices-go/ms-ingest/internal/adapters/httpapi"
	"github.com/MarceloVilela/microservices-go/ms-ingest/internal/adapters/postgres"
	"github.com/MarceloVilela/microservices-go/ms-ingest/internal/app"
	"github.com/MarceloVilela/microservices-go/platform/amqpx"
	"github.com/MarceloVilela/microservices-go/platform/config"
	"github.com/MarceloVilela/microservices-go/platform/db"
	"github.com/MarceloVilela/microservices-go/platform/httpx"
	"github.com/MarceloVilela/microservices-go/platform/metrics"
	"github.com/MarceloVilela/microservices-go/platform/outbox"
	"github.com/MarceloVilela/microservices-go/platform/service"
	"github.com/MarceloVilela/microservices-go/platform/topology"
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	os.Exit(service.Run(ctx, spec(), os.Args[1:], os.Environ(), os.Stdout, os.Stderr))
}

func positiveInt(s string) error {
	if n, err := strconv.Atoi(s); err != nil || n < 1 || n > 1000 {
		return config.ErrInvalid("inteiro de 1 a 1000")
	}
	return nil
}

func spec() service.Spec {
	reg := metrics.NewRegistry()
	return service.Spec{
		Metrics:     reg,
		Name:        "ms-ingest",
		DefaultAddr: ":8081",
		Require:     amqpx.Requirement{Exchange: true, Queues: []topology.Queue{topology.PhotoProcess, topology.BatchCompleted}},
		ExtraVars: []config.Var{
			{Name: "DATABASE_URL", Required: true, Secret: true, Validate: db.ValidURL},
			{Name: "ALLOWED_HOSTS", Default: "picsum.photos,fastly.picsum.photos"},
			{Name: "CORS_ORIGINS"},
			{Name: "DEMO_TOKEN", Secret: true},
			{Name: "RELAY_POLL_INTERVAL", Default: "500ms", Validate: config.ValidDuration},
			{Name: "RELAY_BATCH_SIZE", Default: "100", Validate: positiveInt},
			{Name: "RELAY_MAX_ATTEMPTS", Default: "10", Validate: positiveInt},
		},
		Setup: func(ctx context.Context, vals config.Values, log *slog.Logger) (*service.Runtime, error) {
			return setup(ctx, vals, log, reg)
		},
	}
}

func split(s string) []string {
	var out []string
	for _, p := range strings.Split(s, ",") {
		if p = strings.ToLower(strings.TrimSpace(p)); p != "" {
			out = append(out, p)
		}
	}
	return out
}

func setup(ctx context.Context, vals config.Values, log *slog.Logger, reg *metrics.Registry) (*service.Runtime, error) {
	pool, err := db.Open(ctx, vals.String("DATABASE_URL"))
	if err != nil {
		return nil, err
	}
	if err := db.Migrate(ctx, pool); err != nil {
		pool.Close()
		return nil, err
	}

	svc := app.Service{Store: postgres.Store{Pool: pool}, Allowed: split(vals.String("ALLOWED_HOSTS"))}
	cors := httpx.CORS(origins(vals.String("CORS_ORIGINS")))
	handler := httpx.Chain(httpapi.Handler{Creator: svc, Log: log}, cors, httpx.Token(vals.String("DEMO_TOKEN")))

	pub := &amqpx.Publisher{URL: vals.String("AMQP_URL")}
	relay := &outbox.Relay{
		Store: outbox.PgStore{Pool: pool},
		Publisher: outbox.PublisherFunc(func(ctx context.Context, m outbox.Message) error {
			err := pub.Publish(ctx, m.RoutingKey, m.MessageID, m.Payload)
			if errors.Is(err, amqpx.ErrUnroutable) {
				// culpa da linha (chave sem fila), não do broker: conta para a quarentena
				return fmt.Errorf("%w: %w", outbox.ErrRejected, err)
			}
			return err
		}),
		MaxAttempts:  func() int { n, _ := strconv.Atoi(vals.String("RELAY_MAX_ATTEMPTS")); return n }(),
		BatchSize:    func() int { n, _ := strconv.Atoi(vals.String("RELAY_BATCH_SIZE")); return n }(),
		PollInterval: vals.Duration("RELAY_POLL_INTERVAL"),
		Log:          log,
	}

	// uma consulta por scrape (cache de 5 s) alimenta os três gauges
	stats := metrics.NewCache(func() (map[string]float64, error) {
		qctx, cancel := context.WithTimeout(ctx, 3*time.Second)
		defer cancel()
		st, err := outbox.PgStore{Pool: pool}.Stats(qctx)
		return map[string]float64{"pending": st.Pending, "quarantined": st.Quarantined, "oldest_seconds": st.OldestSeconds}, err
	}, 5*time.Second)
	for _, g := range []struct{ name, key, help string }{
		{"outbox_pending", "pending", "Linhas do outbox ainda não publicadas (fora da quarentena)."},
		{"outbox_quarantined", "quarantined", "Linhas do outbox em quarentena (ADR 0021): exigem ação manual."},
		{"outbox_oldest_pending_age_seconds", "oldest_seconds", "Idade da linha pendente mais antiga do outbox (0 sem pendentes)."},
	} {
		reg.Gauge(g.name, g.help, func() (float64, error) {
			v, err := stats.Get()
			return v[g.key], err
		})
	}

	return &service.Runtime{
		Mount: func(mux *http.ServeMux) {
			mux.Handle("POST /batches", handler)
			// preflight: o middleware de CORS responde; sem CORS (ou origem não permitida), 405
			mux.Handle("OPTIONS /batches", cors(http.HandlerFunc(
				func(w http.ResponseWriter, _ *http.Request) {
					httpx.Problem(w, http.StatusMethodNotAllowed, "method_not_allowed", "use POST")
				})))
		},
		Ready:      pool.Ping,
		Background: []func(context.Context) error{relay.Run},
		Close:      func() { pub.Close(); pool.Close() },
	}, nil
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
