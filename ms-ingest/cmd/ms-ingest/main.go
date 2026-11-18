// Comando ms-ingest.
package main

import (
	"context"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"syscall"

	"github.com/MarceloVilela/microservices-go/ms-ingest/internal/adapters/httpapi"
	"github.com/MarceloVilela/microservices-go/ms-ingest/internal/adapters/postgres"
	"github.com/MarceloVilela/microservices-go/ms-ingest/internal/app"
	"github.com/MarceloVilela/microservices-go/platform/amqpx"
	"github.com/MarceloVilela/microservices-go/platform/config"
	"github.com/MarceloVilela/microservices-go/platform/db"
	"github.com/MarceloVilela/microservices-go/platform/httpx"
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
	return service.Spec{
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
		},
		Setup: setup,
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

func setup(ctx context.Context, vals config.Values, log *slog.Logger) (*service.Runtime, error) {
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
			return pub.Publish(ctx, m.RoutingKey, strconv.FormatInt(m.ID, 10), m.Payload)
		}),
		BatchSize:    func() int { n, _ := strconv.Atoi(vals.String("RELAY_BATCH_SIZE")); return n }(),
		PollInterval: vals.Duration("RELAY_POLL_INTERVAL"),
		Log:          log,
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
