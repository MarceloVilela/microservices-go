// Comando topology-init: declara exchange, filas, degraus de retry e DLQ.
package main

import (
	"context"
	"os"
	"os/signal"
	"syscall"

	"github.com/MarceloVilela/microservices-go/ms-queues/internal/initcmd"
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	os.Exit(initcmd.Run(ctx, os.Environ(), os.Stdout, os.Stderr))
}
