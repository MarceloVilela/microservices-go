// Spike S1: publisher confirms e reconexão com amqp091-go contra RabbitMQ 4.
// Modos: latency (1.000 publicações com confirm), reconnect (reinício do broker no meio).
package main

import (
	"context"
	"fmt"
	"os"
	"sort"
	"time"

	amqp "github.com/rabbitmq/amqp091-go"
)

var queue = "s1.photo.process"

func init() {
	if os.Getenv("QUEUE_TYPE") == "quorum" {
		queue = "s1.photo.process.quorum"
	}
}

func url() string {
	if u := os.Getenv("AMQP_URL"); u != "" {
		return u
	}
	return "amqp://admin:admin@127.0.0.1:5673/"
}

func declare(ch *amqp.Channel) error {
	var args amqp.Table
	if os.Getenv("QUEUE_TYPE") == "quorum" {
		args = amqp.Table{"x-queue-type": "quorum"}
	}
	_, err := ch.QueueDeclare(queue, true, false, false, false, args)
	return err
}

func pct(d []time.Duration, p float64) time.Duration {
	s := append([]time.Duration(nil), d...)
	sort.Slice(s, func(i, j int) bool { return s[i] < s[j] })
	return s[int(float64(len(s)-1)*p)]
}

func latency() error {
	conn, err := amqp.Dial(url())
	if err != nil {
		return err
	}
	defer conn.Close()
	ch, _ := conn.Channel()
	if err := declare(ch); err != nil {
		return err
	}
	if err := ch.Confirm(false); err != nil {
		return err
	}
	ctx := context.Background()
	body := []byte(`{"v":1,"batch_id":"b","photo_id":"p"}`)
	const n = 1000

	// 1) um confirm por mensagem (publica e espera)
	lat := make([]time.Duration, 0, n)
	t0 := time.Now()
	for i := 0; i < n; i++ {
		s := time.Now()
		dc, err := ch.PublishWithDeferredConfirmWithContext(ctx, "", queue, false, false,
			amqp.Publishing{DeliveryMode: amqp.Persistent, ContentType: "application/json", Body: body})
		if err != nil {
			return err
		}
		if ok, err := dc.WaitContext(ctx); err != nil || !ok {
			return fmt.Errorf("nack/err: %v %v", ok, err)
		}
		lat = append(lat, time.Since(s))
	}
	tot := time.Since(t0)
	fmt.Printf("sequencial: n=%d total=%v throughput=%.0f/s p50=%v p95=%v p99=%v max=%v\n",
		n, tot.Round(time.Millisecond), float64(n)/tot.Seconds(), pct(lat, .5), pct(lat, .95), pct(lat, .99), pct(lat, 1))

	// 2) publica tudo e confirma em lote no fim
	t0 = time.Now()
	dcs := make([]*amqp.DeferredConfirmation, 0, n)
	for i := 0; i < n; i++ {
		dc, err := ch.PublishWithDeferredConfirmWithContext(ctx, "", queue, false, false,
			amqp.Publishing{DeliveryMode: amqp.Persistent, ContentType: "application/json", Body: body})
		if err != nil {
			return err
		}
		dcs = append(dcs, dc)
	}
	for _, dc := range dcs {
		if ok, err := dc.WaitContext(ctx); err != nil || !ok {
			return fmt.Errorf("nack/err: %v %v", ok, err)
		}
	}
	tot = time.Since(t0)
	fmt.Printf("em lote:    n=%d total=%v throughput=%.0f/s\n", n, tot.Round(time.Millisecond), float64(n)/tot.Seconds())
	return nil
}

// publisher com reconexão: reenvia a mesma mensagem até o broker confirmar.
type pub struct {
	conn *amqp.Connection
	ch   *amqp.Channel
}

func (p *pub) connect() error {
	var err error
	for attempt, backoff := 1, 200*time.Millisecond; ; attempt, backoff = attempt+1, min(backoff*2, 3*time.Second) {
		if p.conn, err = amqp.Dial(url()); err == nil {
			if p.ch, err = p.conn.Channel(); err == nil {
				if err = declare(p.ch); err == nil {
					if err = p.ch.Confirm(false); err == nil {
						fmt.Printf("  conectado (tentativa %d)\n", attempt)
						return nil
					}
				}
			}
			p.conn.Close()
		}
		time.Sleep(backoff)
	}
}

func (p *pub) publish(body []byte) (retries int) {
	for {
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		dc, err := p.ch.PublishWithDeferredConfirmWithContext(ctx, "", queue, false, false,
			amqp.Publishing{DeliveryMode: amqp.Persistent, ContentType: "application/json", Body: body})
		if err == nil {
			var ok bool
			if ok, err = dc.WaitContext(ctx); err == nil && ok {
				cancel()
				return retries
			}
		}
		cancel()
		retries++
		fmt.Printf("  falha (%v); reconectando\n", err)
		p.conn.Close()
		p.connect()
	}
}

func reconnect() error {
	p := &pub{}
	p.connect()
	const n = 200
	retries := 0
	t0 := time.Now()
	for i := 0; i < n; i++ {
		retries += p.publish([]byte(fmt.Sprintf(`{"i":%d}`, i)))
		time.Sleep(100 * time.Millisecond)
	}
	fmt.Printf("reconnect: confirmadas=%d reenvios=%d duração=%v\n", n, retries, time.Since(t0).Round(time.Millisecond))
	return nil
}

func main() {
	var err error
	switch os.Args[1] {
	case "latency":
		err = latency()
	case "reconnect":
		err = reconnect()
	}
	if err != nil {
		fmt.Println("erro:", err)
		os.Exit(1)
	}
}
