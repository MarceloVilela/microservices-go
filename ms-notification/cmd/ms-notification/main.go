// Comando ms-notification.
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
		Name:        "ms-notification",
		DefaultAddr: ":8084",
		Require:     amqpx.Requirement{Queues: []topology.Queue{topology.BatchCompleted}},
	}
	os.Exit(service.Run(ctx, spec, os.Args[1:], os.Environ(), os.Stdout, os.Stderr))
}
