// Package initcmd implementa o topology-init: o único lugar que declara a
// topologia do broker (ADR 0015).
package initcmd

import (
	"context"
	"fmt"
	"io"
	"time"

	"github.com/MarceloVilela/microservices-go/platform/amqpx"
	"github.com/MarceloVilela/microservices-go/platform/config"
	"github.com/MarceloVilela/microservices-go/platform/logging"
	"github.com/MarceloVilela/microservices-go/platform/topology"
)

// Tempo máximo esperando o broker aceitar conexões (o primeiro start do
// RabbitMQ pode demorar ou reiniciar; ver specs/spikes/s1-amqp.md).
const (
	waitBroker     = 60 * time.Second
	attemptTimeout = 10 * time.Second
)

// Run declara a topologia e devolve o código de saída (0 ok, 1 falha).
func Run(ctx context.Context, environ []string, _, stderr io.Writer) int {
	vals, err := config.Load(environ, []config.Var{
		{Name: "LOG_LEVEL", Default: "info", Validate: config.OneOf("debug", "info", "warn", "error")},
		{Name: "AMQP_URL", Required: true, Secret: true, Validate: amqpx.ValidURL},
	})
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "configuração inválida:\n%v\n", err)
		return 1
	}
	log := logging.New(stderr, "topology-init", vals.String("LOG_LEVEL"))

	ctx, cancel := context.WithTimeout(ctx, waitBroker)
	defer cancel()

	var lastErr error
	for attempt := 1; ; attempt++ {
		// prazo por tentativa: um handshake travado não consome os 60s inteiros
		actx, acancel := context.WithTimeout(ctx, attemptTimeout)
		conn, err := amqpx.Dial(actx, vals.String("AMQP_URL"))
		if err == nil {
			err = amqpx.Declare(actx, conn)
			_ = conn.Close()
			if err == nil {
				names := []string{}
				for _, q := range topology.Queues() {
					names = append(names, q.Name)
				}
				acancel()
				log.Info("topologia declarada", "exchange", topology.Exchange, "filas", names)
				return 0
			}
		}
		acancel()
		if amqpx.IsPermanent(err) {
			// ex.: PRECONDITION_FAILED (argumentos divergentes) ou acesso recusado
			log.Error("falha permanente ao declarar a topologia", "erro", err)
			return 1
		}
		lastErr = err
		log.Warn("broker indisponível ou caiu no meio da declaração, tentando de novo", "tentativa", attempt, "erro", err)
		select {
		case <-ctx.Done():
			log.Error("desisti de esperar o broker", "erro", lastErr)
			return 1
		case <-time.After(2 * time.Second):
		}
	}
}
