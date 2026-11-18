// Spike S2: retry por degraus (TTL fixo + DLX), thundering herd, bloqueio de cabeça de fila
// e retry atrasado nativo das quorum queues (RabbitMQ 4.3).
package main

import (
	"context"
	"fmt"
	"os"
	"sort"
	"strconv"
	"strings"
	"time"

	amqp "github.com/rabbitmq/amqp091-go"
)

const ex = "s2.photos"

func url() string {
	if u := os.Getenv("AMQP_URL"); u != "" {
		return u
	}
	return "amqp://admin:admin@127.0.0.1:5673/"
}

func envInt(k string, d int) int {
	if v, err := strconv.Atoi(os.Getenv(k)); err == nil {
		return v
	}
	return d
}

func must(err error) {
	if err != nil {
		fmt.Println("erro:", err)
		os.Exit(1)
	}
}

type env struct {
	conn *amqp.Connection
	pub  *amqp.Channel // com confirms
	con  *amqp.Channel
}

func setup() *env {
	c, err := amqp.Dial(url())
	must(err)
	pub, _ := c.Channel()
	must(pub.Confirm(false))
	con, _ := c.Channel()
	must(con.Qos(1, 0, false))
	return &env{c, pub, con}
}

func (e *env) publish(key string, h amqp.Table, exp string) {
	ctx := context.Background()
	dc, err := e.pub.PublishWithDeferredConfirmWithContext(ctx, ex, key, false, false, amqp.Publishing{
		DeliveryMode: amqp.Persistent, Headers: h, Expiration: exp, Body: []byte("x")})
	must(err)
	ok, err := dc.WaitContext(ctx)
	if err != nil || !ok {
		must(fmt.Errorf("publish sem confirm: %v %v", ok, err))
	}
}

func (e *env) reset(queues ...string) {
	for _, q := range queues {
		e.con.QueueDelete(q, false, false, false)
		if e.con.IsClosed() {
			e.con, _ = e.conn.Channel()
		}
	}
	must(e.con.ExchangeDeclare(ex, "direct", true, false, false, false, nil))
}

func (e *env) queue(name, key string, args amqp.Table) {
	_, err := e.con.QueueDeclare(name, true, false, false, false, args)
	must(err)
	must(e.con.QueueBind(name, key, ex, false, nil))
}

func pct(d []time.Duration, p float64) time.Duration {
	s := append([]time.Duration(nil), d...)
	sort.Slice(s, func(i, j int) bool { return s[i] < s[j] })
	return s[int(float64(len(s)-1)*p)]
}

type ev struct {
	id      int
	attempt int
	at      time.Time
}

// ladder: N mensagens que sempre falham passam pelos degraus e terminam na DLQ.
func ladder() {
	var steps []time.Duration
	for _, s := range strings.Split(os.Getenv("LADDER"), ",") {
		d, err := time.ParseDuration(s)
		must(err)
		steps = append(steps, d)
	}
	n := envInt("MSGS", 1)
	e := setup()
	qs := []string{"s2.process", "s2.dlq"}
	for i := range steps {
		qs = append(qs, fmt.Sprintf("s2.retry.%d", i))
	}
	e.reset(qs...)
	e.queue("s2.process", "process", nil)
	e.queue("s2.dlq", "dlq", nil)
	for i, d := range steps {
		e.queue(fmt.Sprintf("s2.retry.%d", i), fmt.Sprintf("retry.%d", i), amqp.Table{
			"x-message-ttl": int32(d.Milliseconds()), "x-dead-letter-exchange": ex, "x-dead-letter-routing-key": "process"})
	}
	msgs, err := e.con.Consume("s2.process", "", false, false, false, false, nil)
	must(err)
	dlq, _ := e.conn.Channel()
	dmsgs, err := dlq.Consume("s2.dlq", "", true, false, false, false, nil)
	must(err)

	var evs []ev
	done := make(chan struct{})
	go func() {
		for m := range msgs {
			a := int(m.Headers["attempt"].(int32))
			id := int(m.Headers["id"].(int32))
			evs = append(evs, ev{id, a, time.Now()})
			if a < len(steps) { // falha transitória: degrau a
				e.publish(fmt.Sprintf("retry.%d", a), amqp.Table{"attempt": int32(a + 1), "id": int32(id)}, "")
			} else { // esgotou: DLQ
				e.publish("dlq", amqp.Table{"attempt": int32(a), "id": int32(id)}, "")
			}
			m.Ack(false)
		}
	}()
	t0 := time.Now()
	for i := 0; i < n; i++ {
		e.publish("process", amqp.Table{"attempt": int32(0), "id": int32(i)}, "")
	}
	go func() {
		c := 0
		for range dmsgs {
			if c++; c == n {
				close(done)
				return
			}
		}
	}()
	select {
	case <-done:
	case <-time.After(10*time.Minute + 5*time.Second):
		must(fmt.Errorf("timeout"))
	}
	fmt.Printf("ladder=%v n=%d fim na DLQ após %v\n", steps, n, time.Since(t0).Round(time.Millisecond))
	// por degrau: instante de cada reentrega, relativo à falha anterior
	byID := map[int][]ev{}
	for _, v := range evs {
		byID[v.id] = append(byID[v.id], v)
	}
	for a := 1; a <= len(steps); a++ {
		var delays []time.Duration
		var abs []time.Duration
		for _, es := range byID {
			sort.Slice(es, func(i, j int) bool { return es[i].attempt < es[j].attempt })
			if len(es) > a {
				delays = append(delays, es[a].at.Sub(es[a-1].at))
				abs = append(abs, es[a].at.Sub(t0))
			}
		}
		fmt.Printf("  degrau %d (TTL %v): reentregas=%d atraso medido p50=%v min=%v max=%v | janela de chegada (max-min) = %v\n",
			a, steps[a-1], len(delays), pct(delays, .5).Round(time.Millisecond), pct(delays, 0).Round(time.Millisecond),
			pct(delays, 1).Round(time.Millisecond), (pct(abs, 1) - pct(abs, 0)).Round(time.Millisecond))
		// maior número de reentregas em 100 ms (pico do herd)
		b := map[int64]int{}
		peak := 0
		for _, d := range abs {
			k := d.Milliseconds() / 100
			if b[k]++; b[k] > peak {
				peak = b[k]
			}
		}
		fmt.Printf("    pico: %d reentregas em uma janela de 100 ms (de %d)\n", peak, len(abs))
	}
}

// hol: TTL por mensagem em fila única; A (6 s) na frente de B (1 s).
func hol() {
	e := setup()
	e.reset("s2.hol", "s2.holdlq")
	e.queue("s2.holdlq", "holdlq", nil)
	e.queue("s2.hol", "hol", amqp.Table{"x-dead-letter-exchange": ex, "x-dead-letter-routing-key": "holdlq"})
	dmsgs, _ := e.con.Consume("s2.holdlq", "", true, false, false, false, nil)
	t0 := time.Now()
	e.publish("hol", amqp.Table{"id": int32(1)}, "6000") // A
	e.publish("hol", amqp.Table{"id": int32(2)}, "1000") // B
	for i := 0; i < 2; i++ {
		m := <-dmsgs
		fmt.Printf("  mensagem %v (TTL por mensagem %s) chegou à DLQ em %v\n", m.Headers["id"],
			map[int32]string{1: "6000 ms", 2: "1000 ms"}[m.Headers["id"].(int32)], time.Since(t0).Round(100*time.Millisecond))
	}
}

// native: quorum queue com retry atrasado nativo; o consumidor devolve a mensagem sempre.
func native() {
	typ := os.Getenv("RETRY_TYPE") // all|failed|returned
	how := os.Getenv("HOW")        // reject|nack
	e := setup()
	e.reset("s2.native", "s2.nativedlq")
	e.queue("s2.nativedlq", "nativedlq", nil)
	e.queue("s2.native", "native", amqp.Table{
		"x-queue-type": "quorum", "x-delayed-retry-type": typ,
		"x-delayed-retry-min": int32(500), "x-delayed-retry-max": int32(3000),
		"x-delivery-limit": int32(5), "x-dead-letter-exchange": ex, "x-dead-letter-routing-key": "nativedlq"})
	msgs, err := e.con.Consume("s2.native", "", false, false, false, false, nil)
	must(err)
	dm, _ := e.con.Consume("s2.nativedlq", "", true, false, false, false, nil)
	t0 := time.Now()
	last := t0
	e.publish("native", amqp.Table{"id": int32(1)}, "")
	fmt.Printf("  tipo=%s devolução=%s (min 500 ms, max 3000 ms, delivery-limit 5)\n", typ, how)
	go func() {
		for m := range msgs {
			now := time.Now()
			fmt.Printf("    entrega: +%v desde a anterior (x-delivery-count=%v)\n", now.Sub(last).Round(10*time.Millisecond), m.Headers["x-delivery-count"])
			last = now
			if how == "nack" {
				m.Nack(false, true)
			} else {
				m.Reject(true)
			}
		}
	}()
	select {
	case <-dm:
		fmt.Printf("    DLQ após %v (delivery-limit)\n", time.Since(t0).Round(10*time.Millisecond))
	case <-time.After(60 * time.Second):
		fmt.Println("    timeout: mensagem não chegou à DLQ")
	}
}

func main() {
	switch os.Args[1] {
	case "ladder":
		ladder()
	case "hol":
		hol()
	case "native":
		native()
	}
}
