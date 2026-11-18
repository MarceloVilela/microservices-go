package sse

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/jackc/pgx/v5"
)

// Hub mantém uma conexão dedicada com LISTEN no canal `events` e acorda o Broadcaster a cada
// NOTIFY (o trigger da migração 0004 dispara um por linha de `events`). Se a conexão cai, reconecta
// com backoff e acorda todos os streams para que releiam por seq: nenhum evento depende do NOTIFY.
type Hub struct {
	URL         string
	Broadcaster *Broadcaster
	Log         *slog.Logger
}

// Run escuta até ctx terminar.
func (h *Hub) Run(ctx context.Context) error {
	backoff := time.Second
	for {
		listening, err := h.listen(ctx)
		if listening {
			backoff = time.Second // a sessão chegou a escutar: a próxima queda recomeça do degrau mínimo
		}
		if ctx.Err() != nil {
			return ctx.Err()
		}
		h.Log.Warn("sse: LISTEN caiu; reconectando", "erro", err, "espera", backoff)
		h.Broadcaster.Notify() // reler por seq enquanto o LISTEN não volta
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(backoff):
		}
		backoff = min(backoff*2, 30*time.Second)
	}
}

// listen devolve se o LISTEN chegou a ser estabelecido e o erro que encerrou a sessão.
func (h *Hub) listen(ctx context.Context) (listening bool, err error) {
	conn, err := pgx.Connect(ctx, h.URL)
	if err != nil {
		return false, fmt.Errorf("conectar: %w", err)
	}
	defer func() { _ = conn.Close(context.WithoutCancel(ctx)) }()
	if _, err := conn.Exec(ctx, "LISTEN events"); err != nil {
		return false, fmt.Errorf("LISTEN: %w", err)
	}
	h.Broadcaster.Notify() // eventos gravados antes de o LISTEN começar
	for {
		if _, err := conn.WaitForNotification(ctx); err != nil {
			return true, fmt.Errorf("esperar notificação: %w", err)
		}
		h.Broadcaster.Notify()
	}
}
