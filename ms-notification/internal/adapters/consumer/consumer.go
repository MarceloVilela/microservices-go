// Package consumer liga o app ao RabbitMQ: consome topology.BatchCompleted com ack manual e
// prefetch 1. Consumer.Run é uma sessão; quem a refaz depois de uma queda é o amqpx.Supervisor (ADR 0003).
package consumer

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sync/atomic"
	"time"

	amqp "github.com/rabbitmq/amqp091-go"

	"github.com/MarceloVilela/microservices-go/ms-notification/internal/app"
	"github.com/MarceloVilela/microservices-go/platform/amqpx"
	"github.com/MarceloVilela/microservices-go/platform/topology"
)

// Handler decide o que fazer com o corpo da mensagem (app.Service).
type Handler interface {
	Handle(ctx context.Context, body []byte) app.Action
}

// Consumer consome topology.BatchCompleted, uma mensagem por vez.
type Consumer struct {
	URL string
	// ShutdownTimeout é quanto esperar a mensagem em voo depois do sinal de parada.
	ShutdownTimeout time.Duration
	// RequeueDelay evita laço quente com o banco fora (padrão 1 s).
	RequeueDelay time.Duration
	Handler      Handler
	Log          *slog.Logger

	connected atomic.Bool
}

// Connected diz se há uma sessão consumindo agora (informativo).
func (c *Consumer) Connected() bool { return c.connected.Load() }

const tag = "ms-notification"

// Run é uma sessão de consumo: devolve nil quando ctx termina, erro quando a conexão ou o canal caem.
func (c *Consumer) Run(ctx context.Context) error {
	conn, err := amqpx.Dial(ctx, c.URL)
	if err != nil {
		return fmt.Errorf("conectar ao broker: %w", err)
	}
	defer func() { _ = conn.Close() }()
	ch, err := conn.Channel()
	if err != nil {
		return fmt.Errorf("abrir canal: %w", err)
	}
	if err := ch.Qos(1, 0, false); err != nil {
		return fmt.Errorf("qos: %w", err)
	}
	deliveries, err := ch.ConsumeWithContext(ctx, topology.BatchCompleted.Name, tag, false, false, false, false, nil)
	if err != nil {
		return fmt.Errorf("consumir: %w", err)
	}
	closed := conn.NotifyClose(make(chan *amqp.Error, 1))
	chClosed := ch.NotifyClose(make(chan *amqp.Error, 1))
	c.connected.Store(true)
	defer c.connected.Store(false)

	// a mensagem em voo tem o próprio prazo, que só começa a contar no pedido de parada
	procCtx, procCancel := context.WithCancel(context.WithoutCancel(ctx))
	defer procCancel()
	var deadline atomic.Pointer[time.Timer]
	stopTimer := context.AfterFunc(ctx, func() { deadline.Store(time.AfterFunc(c.ShutdownTimeout, procCancel)) })
	defer func() {
		stopTimer()
		if t := deadline.Load(); t != nil {
			t.Stop()
		}
	}()

	errc := make(chan error, 1)
	done := make(chan struct{})
	go func() {
		defer close(done)
		if err := c.work(ctx, procCtx, deliveries); err != nil {
			errc <- err
		}
	}()
	c.Log.Info("consumindo", "fila", topology.BatchCompleted.Name)

	var runErr error
	select {
	case <-ctx.Done():
	case err := <-errc:
		runErr = err
	case e := <-closed:
		runErr = fmt.Errorf("conexão com o broker caiu: %w", orClosed(e))
	case e := <-chClosed:
		runErr = fmt.Errorf("canal com o broker caiu: %w", orClosed(e))
	}
	if runErr != nil {
		procCancel() // queda: não há o que esperar
	}
	_ = ch.Cancel(tag, false)
	<-done
	return runErr
}

func (c *Consumer) work(ctx, procCtx context.Context, deliveries <-chan amqp.Delivery) error {
	for {
		select {
		case <-ctx.Done():
			return nil
		case d, ok := <-deliveries:
			if !ok {
				if ctx.Err() != nil {
					return nil //nolint:nilerr // desligamento pedido: fechar o canal não é falha
				}
				return errors.New("canal de entregas fechado")
			}
			if c.Handler.Handle(procCtx, d.Body) == app.Requeue {
				c.sleep(procCtx)
				if err := d.Nack(false, true); err != nil {
					return c.ackFailed("nack", d, err)
				}
				continue
			}
			if err := d.Ack(false); err != nil {
				return c.ackFailed("ack", d, err)
			}
		}
	}
}

// ackFailed: ack que não chegou ao broker (canal fechado no voo) não é fatal para o serviço; o
// Supervisor reabre a sessão e o broker reentrega, e a deduplicação absorve.
func (c *Consumer) ackFailed(op string, d amqp.Delivery, err error) error {
	c.Log.Warn("ack não chegou ao broker; a mensagem será reentregue", "op", op, "message_id", d.MessageId, "erro", err)
	return fmt.Errorf("%s: %w", op, err)
}

func (c *Consumer) sleep(ctx context.Context) {
	d := c.RequeueDelay
	if d <= 0 {
		d = time.Second
	}
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-t.C:
	case <-ctx.Done():
	}
}

func orClosed(e *amqp.Error) error {
	if e == nil {
		return errors.New("fechada")
	}
	return e
}
