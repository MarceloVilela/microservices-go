// Comando ms-notification.
package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/MarceloVilela/microservices-go/ms-notification/internal/adapters/consumer"
	"github.com/MarceloVilela/microservices-go/ms-notification/internal/adapters/postgres"
	"github.com/MarceloVilela/microservices-go/ms-notification/internal/adapters/slogn"
	"github.com/MarceloVilela/microservices-go/ms-notification/internal/adapters/smtpn"
	"github.com/MarceloVilela/microservices-go/ms-notification/internal/app"
	"github.com/MarceloVilela/microservices-go/platform/amqpx"
	"github.com/MarceloVilela/microservices-go/platform/config"
	"github.com/MarceloVilela/microservices-go/platform/db"
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
		Name:        "ms-notification",
		DefaultAddr: ":8084",
		Require:     amqpx.Requirement{Queues: []topology.Queue{topology.BatchCompleted}},
		ExtraVars: []config.Var{
			{Name: "DATABASE_URL", Required: true, Secret: true, Validate: db.ValidURL},
			// SMTP é opcional: sem SMTP_ADDR só o slog notifica
			{Name: "SMTP_ADDR"},
			{Name: "SMTP_FROM"},
			{Name: "SMTP_TO"},
			{Name: "SMTP_TIMEOUT", Default: "5s", Validate: config.ValidDuration},
			{Name: "SHUTDOWN_TIMEOUT", Default: "10s", Validate: config.ValidDuration},
			{Name: "CONSUMER_BACKOFF_MAX", Default: "30s", Validate: config.ValidDuration},
		},
		Setup: setup,
	}
}

// notifiers monta os destinos: slog sempre; SMTP só com SMTP_ADDR (e então From e To são obrigatórios).
func notifiers(vals config.Values, log *slog.Logger) ([]app.Notifier, error) {
	out := []app.Notifier{slogn.Notifier{Log: log}}
	if vals.String("SMTP_ADDR") == "" {
		return out, nil
	}
	if vals.String("SMTP_FROM") == "" || vals.String("SMTP_TO") == "" {
		return nil, errors.New("SMTP_ADDR exige SMTP_FROM e SMTP_TO")
	}
	return append(out, smtpn.Notifier{
		Addr: vals.String("SMTP_ADDR"), From: vals.String("SMTP_FROM"), To: vals.String("SMTP_TO"),
		Timeout: vals.Duration("SMTP_TIMEOUT"),
	}), nil
}

func setup(ctx context.Context, vals config.Values, log *slog.Logger) (*service.Runtime, error) {
	ns, err := notifiers(vals, log)
	if err != nil {
		return nil, err
	}
	pool, err := db.Open(ctx, vals.String("DATABASE_URL"))
	if err != nil {
		return nil, err
	}
	// não é dono do esquema: só verifica (o ms-ingest migra; ADR 0007)
	svc := &app.Service{Store: postgres.Store{Pool: pool}, Notifiers: ns, Log: log}
	cons := &consumer.Consumer{
		URL: vals.String("AMQP_URL"), ShutdownTimeout: vals.Duration("SHUTDOWN_TIMEOUT"), Handler: svc, Log: log,
	}
	backoffMax := max(vals.Duration("CONSUMER_BACKOFF_MAX"), time.Second)
	sup := &amqpx.Supervisor{
		Run: cons.Run, BackoffMin: min(time.Second, backoffMax), BackoffMax: backoffMax, StableAfter: 30 * time.Second, Log: log,
	}
	names := make([]string, 0, len(ns))
	for _, n := range ns {
		names = append(names, n.Name())
	}
	log.Info("destinos de notificação", "destinos", names)

	return &service.Runtime{
		Mount: func(mux *http.ServeMux) {
			mux.HandleFunc("GET /statusz", func(w http.ResponseWriter, _ *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				_, _ = fmt.Fprintf(w, `{"consumer_connected":%t}`+"\n", cons.Connected())
			})
		},
		Ready: func(ctx context.Context) error {
			return db.Verify(ctx, pool) // já consulta o banco: dispensa um Ping
		},
		Background: []func(context.Context) error{func(ctx context.Context) error {
			if err := waitSchema(ctx, pool, log); err != nil {
				return err
			}
			return sup.Serve(ctx)
		}},
		Close: pool.Close,
	}, nil
}

// waitSchema espera o ms-ingest migrar; consumir antes só geraria reentregas.
func waitSchema(ctx context.Context, pool *pgxpool.Pool, log *slog.Logger) error {
	for {
		err := db.Verify(ctx, pool)
		if err == nil {
			return nil
		}
		log.Warn("esperando o esquema do banco", "erro", err)
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(2 * time.Second):
		}
	}
}
