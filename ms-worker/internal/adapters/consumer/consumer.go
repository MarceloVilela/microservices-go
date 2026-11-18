// Package consumer liga o app ao RabbitMQ: consumo com ack manual, N goroutines e as
// republicações (degrau de retry e DLQ) com confirm. Consumer.Run é uma sessão; quem a refaz
// depois de uma queda é o Supervisor (ADR 0003).
package consumer

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"math"
	"sync"
	"sync/atomic"
	"time"

	amqp "github.com/rabbitmq/amqp091-go"

	"github.com/MarceloVilela/microservices-go/ms-worker/internal/app"
	"github.com/MarceloVilela/microservices-go/ms-worker/internal/domain"
	"github.com/MarceloVilela/microservices-go/platform/amqpx"
	"github.com/MarceloVilela/microservices-go/platform/messages"
	"github.com/MarceloVilela/microservices-go/platform/topology"
)

// Handler processa uma mensagem e diz o que fazer com o ack (app.Processor).
type Handler interface {
	Handle(ctx context.Context, d app.Delivery) app.Action
}

// Consumer consome topology.PhotoProcess.
type Consumer struct {
	URL         string
	Prefetch    int
	Concurrency int
	// ShutdownTimeout é quanto esperar as mensagens em voo depois do sinal de parada.
	ShutdownTimeout time.Duration
	// RequeueDelay evita laço quente quando a infraestrutura está fora (padrão 1 s).
	RequeueDelay time.Duration
	Handler      Handler
	Log          *slog.Logger

	connected atomic.Bool
}

// Connected diz se há uma sessão consumindo agora (informativo; ver F4-8).
func (c *Consumer) Connected() bool { return c.connected.Load() }

// Run é uma sessão de consumo: até ctx terminar (devolve nil) ou a conexão ou o canal caírem (erro). No desligamento
// espera as mensagens em voo por ShutdownTimeout; o que não terminou volta à fila (sem ack).
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
	// com prefetch menor que a concorrência, goroutines ficariam ociosas
	if err := ch.Qos(max(c.Prefetch, c.Concurrency), 0, false); err != nil {
		return fmt.Errorf("qos: %w", err)
	}
	const tag = "ms-worker"
	deliveries, err := ch.ConsumeWithContext(ctx, topology.PhotoProcess.Name, tag, false, false, false, false, nil)
	if err != nil {
		return fmt.Errorf("consumir: %w", err)
	}
	closed := conn.NotifyClose(make(chan *amqp.Error, 1))
	chClosed := ch.NotifyClose(make(chan *amqp.Error, 1)) // o broker pode fechar só o canal
	c.connected.Store(true)
	defer c.connected.Store(false)

	// as mensagens em voo têm o próprio prazo, que só começa a contar no pedido de parada
	procCtx, procCancel := context.WithCancel(context.WithoutCancel(ctx))
	defer procCancel()
	stopTimer := context.AfterFunc(ctx, func() {
		time.AfterFunc(c.ShutdownTimeout, procCancel)
	})
	defer stopTimer()

	var wg sync.WaitGroup
	errc := make(chan error, c.Concurrency)
	for range c.Concurrency {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if err := c.work(ctx, procCtx, deliveries); err != nil {
				errc <- err
			}
		}()
	}
	c.Log.Info("consumindo", "fila", topology.PhotoProcess.Name, "concorrencia", c.Concurrency, "prefetch", max(c.Prefetch, c.Concurrency))

	var runErr error
	select {
	case <-ctx.Done():
	case err := <-errc:
		runErr = err
	case e := <-closed:
		runErr = fmt.Errorf("conexão com o broker caiu: %w", errOrClosed(e))
	case e := <-chClosed:
		runErr = fmt.Errorf("canal com o broker caiu: %w", errOrClosed(e))
	}
	if runErr != nil {
		procCancel() // falha de infraestrutura: não há o que esperar
	}
	_ = ch.Cancel(tag, false) // para de receber; o que já chegou e não foi processado volta à fila ao fechar
	wg.Wait()
	return runErr
}

// work processa deliveries até ctx terminar ou o canal fechar.
func (c *Consumer) work(ctx, procCtx context.Context, deliveries <-chan amqp.Delivery) error {
	for {
		select {
		case <-ctx.Done():
			return nil
		case d, ok := <-deliveries:
			if !ok {
				return closedErr(ctx)
			}
			act := c.Handler.Handle(procCtx, app.Delivery{Body: d.Body, MessageID: d.MessageId, Attempt: attempt(d.Headers)})
			if act == app.Requeue {
				c.sleep(procCtx)
				if err := d.Nack(false, true); err != nil {
					return fmt.Errorf("nack: %w", err)
				}
				continue
			}
			if err := d.Ack(false); err != nil {
				return c.ackFailed("ack", d, err)
			}
		}
	}
}

// ackFailed trata ack/nack que não chegou ao broker (canal fechado no meio do voo). Não é fatal
// para o serviço: a sessão termina, o Supervisor reabre e o broker reentrega a mensagem, que a
// ordem da ADR 0006 absorve (F4-2).
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

func errOrClosed(e *amqp.Error) error {
	if e == nil {
		return errors.New("fechada")
	}
	return e
}

// attempt lê x-attempt (o AMQP entrega inteiros de vários tamanhos).
func attempt(h amqp.Table) int {
	switch v := h[messages.HeaderAttempt].(type) {
	case int:
		return v
	case int8:
		return int(v)
	case int16:
		return int(v)
	case int32:
		return int(v)
	case int64:
		return int(v)
	case uint8:
		return int(v)
	}
	return 0
}

// Router implementa app.Router sobre um amqpx.Publisher (publicação com confirm).
type Router struct{ Publisher *amqpx.Publisher }

// Retry manda d para o degrau step.
func (r Router) Retry(ctx context.Context, step int, d app.Delivery) error {
	steps := topology.RetrySteps()
	if step < 0 || step >= len(steps) {
		return fmt.Errorf("degrau %d inexistente", step)
	}
	return r.Publisher.PublishWithHeaders(ctx, steps[step].Key, d.MessageID,
		amqp.Table{messages.HeaderAttempt: toInt32(step + 1)}, d.Body)
}

// DeadLetter manda d para a DLQ com a causa nos cabeçalhos.
func (r Router) DeadLetter(ctx context.Context, d app.Delivery, class domain.Class, reason string) error {
	return r.Publisher.PublishWithHeaders(ctx, topology.PhotoDLQ.Key, d.MessageID, amqp.Table{
		messages.HeaderAttempt:       toInt32(d.Attempt),
		messages.HeaderFailureClass:  string(class),
		messages.HeaderFailureReason: reason,
	}, d.Body)
}

// toInt32 limita n ao intervalo de int32 (o degrau e as tentativas são números pequenos).
func toInt32(n int) int32 {
	return int32(max(0, min(n, math.MaxInt32))) //nolint:gosec // limitado na linha acima
}

// closedErr: o canal de entregas fechou. No desligamento é o esperado (sem erro); fora dele, é queda.
func closedErr(ctx context.Context) error {
	if ctx.Err() != nil {
		return nil //nolint:nilerr // desligamento pedido: fechar o canal não é falha
	}
	return errors.New("canal de entregas fechado")
}
