// Package slogn é o destino padrão da notificação: uma linha de log estruturada (ADR 0020).
package slogn

import (
	"context"
	"log/slog"

	"github.com/MarceloVilela/microservices-go/platform/messages"
)

// Notifier escreve o fim do lote no log.
type Notifier struct{ Log *slog.Logger }

// Name identifica o destino nos logs de falha.
func (Notifier) Name() string { return "slog" }

// Notify registra "lote concluído" com os totais.
func (n Notifier) Notify(ctx context.Context, m messages.BatchCompleted) error {
	n.Log.InfoContext(ctx, "lote concluído", "evento", "batch.completed", "batch_id", m.BatchID,
		"total", m.Total, "failed", m.Failed, "done", m.Total-m.Failed)
	return nil
}
