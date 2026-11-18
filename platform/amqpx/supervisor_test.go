package amqpx_test

import (
	"context"
	"errors"
	"log/slog"
	"sync"
	"testing"
	"time"

	"github.com/MarceloVilela/microservices-go/platform/amqpx"
)

// fakeClock deixa o teste controlar a espera sem dormir de verdade.
type waits struct {
	mu sync.Mutex
	d  []time.Duration
}

func (w *waits) add(d time.Duration) { w.mu.Lock(); w.d = append(w.d, d); w.mu.Unlock() }

func newSup(run func(context.Context) error, w *waits, now func() time.Time) *amqpx.Supervisor {
	return &amqpx.Supervisor{
		Run: run, BackoffMin: time.Second, BackoffMax: 8 * time.Second, StableAfter: 30 * time.Second,
		Sleep: func(ctx context.Context, d time.Duration) { w.add(d) },
		Now:   now, Log: slog.New(slog.DiscardHandler),
	}
}

func TestSupervisorReabreAposQuedaEDobraOBackoff(t *testing.T) {
	var w waits
	calls := 0
	ctx, cancel := context.WithCancel(context.Background())
	run := func(context.Context) error {
		calls++
		if calls == 6 {
			cancel()
			return nil
		}
		return errors.New("conexão caiu")
	}
	s := newSup(run, &w, time.Now)
	if err := s.Serve(ctx); err != nil {
		t.Fatal(err)
	}
	want := []time.Duration{1, 2, 4, 8, 8} // dobra até o teto
	for i, d := range want {
		if w.d[i] != d*time.Second {
			t.Errorf("espera %d = %v, quer %v", i, w.d[i], d*time.Second)
		}
	}
	if calls != 6 {
		t.Errorf("chamadas = %d", calls)
	}
}

func TestSupervisorReiniciaOBackoffDepoisDeConexaoEstavel(t *testing.T) {
	var w waits
	clock := time.Unix(0, 0)
	calls := 0
	ctx, cancel := context.WithCancel(context.Background())
	run := func(context.Context) error {
		calls++
		switch calls {
		case 1, 2: // quedas rápidas
		case 3:
			clock = clock.Add(time.Minute) // sessão longa
		default:
			cancel()
			return nil
		}
		return errors.New("caiu")
	}
	s := newSup(run, &w, func() time.Time { return clock })
	_ = s.Serve(ctx)
	want := []time.Duration{time.Second, 2 * time.Second, time.Second}
	for i, d := range want {
		if w.d[i] != d {
			t.Errorf("espera %d = %v, quer %v (%v)", i, w.d[i], d, w.d)
		}
	}
}

func TestSupervisorSaiComContextoCancelado(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	run := func(ctx context.Context) error { <-ctx.Done(); return nil }
	s := newSup(run, &waits{}, time.Now)
	done := make(chan error, 1)
	go func() { done <- s.Serve(ctx) }()
	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("não saiu")
	}
}

func TestSupervisorBrokerForaNoBootEspera(t *testing.T) {
	// o primeiro Run falha (broker fora) e o serviço continua: não devolve erro
	var w waits
	calls := 0
	ctx, cancel := context.WithCancel(context.Background())
	run := func(context.Context) error {
		calls++
		if calls == 3 {
			cancel()
			return nil
		}
		return errors.New("conectar ao broker: recusado")
	}
	if err := newSup(run, &w, time.Now).Serve(ctx); err != nil {
		t.Fatal(err)
	}
	if len(w.d) != 2 {
		t.Errorf("esperas = %v", w.d)
	}
}
