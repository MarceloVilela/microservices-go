// Comando ms-worker.
package main

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/MarceloVilela/microservices-go/ms-worker/internal/adapters/consumer"
	"github.com/MarceloVilela/microservices-go/ms-worker/internal/adapters/filestore"
	"github.com/MarceloVilela/microservices-go/ms-worker/internal/adapters/httpfetch"
	"github.com/MarceloVilela/microservices-go/ms-worker/internal/adapters/postgres"
	"github.com/MarceloVilela/microservices-go/ms-worker/internal/adapters/thumb"
	"github.com/MarceloVilela/microservices-go/ms-worker/internal/app"
	"github.com/MarceloVilela/microservices-go/ms-worker/internal/fault"
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

func intRange(lo, hi int) func(string) error {
	return func(s string) error {
		if n, err := strconv.Atoi(s); err != nil || n < lo || n > hi {
			return config.ErrInvalid(fmt.Sprintf("inteiro de %d a %d", lo, hi))
		}
		return nil
	}
}

func spec() service.Spec {
	return service.Spec{
		Name:        "ms-worker",
		DefaultAddr: ":8083",
		Require:     amqpx.Requirement{Exchange: true, Queues: append(append([]topology.Queue{topology.PhotoProcess}, topology.RetrySteps()...), topology.PhotoDLQ)},
		ExtraVars: []config.Var{
			{Name: "DATABASE_URL", Required: true, Secret: true, Validate: db.ValidURL},
			{Name: "WORKER_CONCURRENCY", Default: "1", Validate: intRange(1, 64)},
			{Name: "WORKER_PREFETCH", Default: "1", Validate: intRange(1, 1000)},
			{Name: "THUMBS_DIR", Default: "/data/thumbs"},
			{Name: "ALLOWED_HOSTS", Default: "picsum.photos,fastly.picsum.photos"},
			{Name: "FETCH_TIMEOUT", Default: "10s", Validate: config.ValidDuration},
			{Name: "MAX_IMAGE_BYTES", Default: "10485760", Validate: intRange(1, 1<<30)},
			{Name: "MAX_PIXELS", Default: "25000000", Validate: intRange(1, 1<<31-1)},
			{Name: "SHUTDOWN_TIMEOUT", Default: "10s", Validate: config.ValidDuration},
		},
		Setup: setup,
	}
}

func atoi(vals config.Values, name string) int { n, _ := strconv.Atoi(vals.String(name)); return n }

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
	// o worker não é dono do esquema: só verifica (o ms-ingest migra; ADR 0007)
	files := filestore.Store{Dir: vals.String("THUMBS_DIR")}
	if n, err := files.CleanTmp(time.Minute); err != nil {
		log.Warn("limpar temporários órfãos", "erro", err)
	} else if n > 0 {
		log.Info("temporários órfãos removidos", "n", n)
	}

	relaxed := fault.Relaxed() // só existe com a build faultinject
	if relaxed {
		log.Warn("MODO DE TESTE: loopback e HTTP liberados no download")
	}
	pub := &amqpx.Publisher{URL: vals.String("AMQP_URL")}
	proc := &app.Processor{
		Photos: postgres.Store{Pool: pool, NewID: postgres.DefaultNewID},
		Fetcher: httpfetch.New(httpfetch.Config{
			AllowedHosts: split(vals.String("ALLOWED_HOSTS")), Timeout: vals.Duration("FETCH_TIMEOUT"),
			MaxBytes: int64(atoi(vals, "MAX_IMAGE_BYTES")), MaxRedirects: 3,
			AllowAnyPort: relaxed, AllowHTTP: relaxed, AllowPrivateIP: relaxed,
		}),
		Thumbnailer: thumb.Maker{MaxPixels: atoi(vals, "MAX_PIXELS")},
		Thumbs:      files,
		Router:      consumer.Router{Publisher: pub},
		Steps:       len(topology.RetrySteps()),
		Log:         log,
	}
	cons := &consumer.Consumer{
		URL: vals.String("AMQP_URL"), Prefetch: atoi(vals, "WORKER_PREFETCH"), Concurrency: atoi(vals, "WORKER_CONCURRENCY"),
		ShutdownTimeout: vals.Duration("SHUTDOWN_TIMEOUT"), Handler: proc, Log: log,
	}

	return &service.Runtime{
		Ready: func(ctx context.Context) error {
			if err := pool.Ping(ctx); err != nil {
				return err
			}
			if err := db.Verify(ctx, pool); err != nil {
				return err
			}
			return files.Check()
		},
		Background: []func(context.Context) error{func(ctx context.Context) error {
			if err := waitSchema(ctx, pool, log); err != nil {
				return err
			}
			return cons.Run(ctx)
		}},
		Close: func() { pub.Close(); pool.Close() },
	}, nil
}

// waitSchema espera o ms-ingest migrar o esquema; consumir antes só geraria reentregas.
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
