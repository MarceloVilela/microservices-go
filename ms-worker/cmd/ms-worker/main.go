// Comando ms-worker.
package main

import (
	"context"
	"os"
	"os/signal"
	"syscall"

	"github.com/MarceloVilela/microservices-go/platform/amqpx"
	"github.com/MarceloVilela/microservices-go/platform/service"
	"github.com/MarceloVilela/microservices-go/platform/topology"
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	spec := service.Spec{
		Name:        "ms-worker",
		DefaultAddr: ":8083",
		Require:     amqpx.Requirement{Exchange: true, Queues: append(append([]topology.Queue{topology.PhotoProcess}, topology.RetrySteps()...), topology.PhotoDLQ)},
	}
	os.Exit(service.Run(ctx, spec, os.Args[1:], os.Environ(), os.Stdout, os.Stderr))
}
