package consumer

import (
	"context"
	"log/slog"
	"time"
)

// Supervisor mantém o consumo vivo: roda Run (uma sessão de conexão, canal e consumo) e, se ela
// terminar por queda, espera um backoff e abre outra (ADR 0003). Só sai com ctx cancelado, então
// um broker fora no boot ou reiniciado depois não derruba o serviço.
type Supervisor struct {
	// Run é uma sessão: devolve nil só quando ctx terminou; erro é queda.
	Run func(ctx context.Context) error
	// BackoffMin e BackoffMax limitam a espera entre sessões (dobra a cada queda seguida).
	BackoffMin, BackoffMax time.Duration
	// StableAfter: uma sessão que durou isto ou mais reinicia o backoff.
	StableAfter time.Duration
	// Sleep e Now existem para teste; os padrões são o relógio real.
	Sleep func(ctx context.Context, d time.Duration)
	Now   func() time.Time
	Log   *slog.Logger
}

// Serve roda as sessões até ctx terminar e devolve nil.
func (s *Supervisor) Serve(ctx context.Context) error {
	sleep, now := s.Sleep, s.Now
	if sleep == nil {
		sleep = sleepCtx
	}
	if now == nil {
		now = time.Now
	}
	backoff := s.BackoffMin
	for {
		start := now()
		err := s.Run(ctx)
		if ctx.Err() != nil {
			return nil //nolint:nilerr // ctx cancelado: a queda da sessão é o desligamento, não falha
		}
		if now().Sub(start) >= s.StableAfter {
			backoff = s.BackoffMin
		}
		s.Log.Warn("consumo interrompido; reconectando", "erro", err, "espera", backoff)
		sleep(ctx, backoff)
		backoff = min(backoff*2, s.BackoffMax)
	}
}

func sleepCtx(ctx context.Context, d time.Duration) {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-t.C:
	case <-ctx.Done():
	}
}
