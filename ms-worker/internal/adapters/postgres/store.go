// Package postgres implementa app.Photos: lê o estado da foto e grava o resultado
// (foto, contador do lote, fim do lote no outbox e eventos) numa só transação.
package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/MarceloVilela/microservices-go/ms-worker/internal/app"
	"github.com/MarceloVilela/microservices-go/platform/messages"
)

// BatchCompletedKey é a chave de roteamento do evento de fim de lote.
const BatchCompletedKey = "batch.completed"

// Store implementa app.Photos.
type Store struct {
	Pool *pgxpool.Pool
	// NewID gera o message_id do batch.completed (UUID).
	NewID func() string
}

// Status devolve o status da foto.
func (s Store) Status(ctx context.Context, photoID string) (string, error) {
	var status string
	err := s.Pool.QueryRow(ctx, `SELECT status FROM photos WHERE id = $1`, photoID).Scan(&status)
	switch {
	case errors.Is(err, pgx.ErrNoRows):
		return "", app.ErrNotFound
	case err != nil:
		return "", fmt.Errorf("ler foto: %w", err)
	}
	return status, nil
}

// Finish é o passo 4 da ordem de operações (ADR 0006 e 0014).
//
// A guarda `status = 'pending'` no UPDATE da foto é o que dá a idempotência: a reentrega (ou
// uma réplica concorrente) encontra 0 linhas e não conta de novo. O UPDATE do lote trava a
// linha dele até o commit, então duas fotos terminando juntas se enfileiram e só uma enxerga
// completed_count = total; `completed_at IS NULL` garante que o outbox receba um único batch.completed.
func (s Store) Finish(ctx context.Context, f app.Finish) (app.Outcome, error) {
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return app.Outcome{}, fmt.Errorf("abrir transação: %w", err)
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()

	var batchID string
	var closed int
	var class, reason *string
	if f.FailClass != "" {
		class, reason = &f.FailClass, &f.FailReason
	}
	err = tx.QueryRow(ctx, `UPDATE photos SET status = $2, failure_class = $3, failure_reason = $4, finished_at = now(),
		attempts_closed = attempts_closed + 1
		WHERE id = $1 AND status = 'pending' RETURNING batch_id, attempts_closed`, f.PhotoID, f.Status, class, reason).Scan(&batchID, &closed)
	if errors.Is(err, pgx.ErrNoRows) {
		return app.Outcome{Duplicate: true}, nil // já terminada: nada a contar (o Rollback do defer encerra)
	}
	if err != nil {
		return app.Outcome{}, fmt.Errorf("atualizar foto: %w", err)
	}

	eventType := "photo." + f.Status
	payload, _ := json.Marshal(map[string]string{"status": f.Status, "failure_class": f.FailClass, "failure_reason": f.FailReason})
	if _, err := tx.Exec(ctx, `INSERT INTO events (type, batch_id, photo_id, payload) VALUES ($1, $2, $3, $4)`,
		eventType, batchID, f.PhotoID, payload); err != nil {
		return app.Outcome{}, fmt.Errorf("gravar evento da foto: %w", err)
	}

	if closed > 1 {
		// foto reaberta pelo ms-api (POST /dlq/{id}/retry): já contou no lote na 1ª finalização (ADR 0018, D3)
		if err := tx.Commit(ctx); err != nil {
			return app.Outcome{}, fmt.Errorf("commit: %w", err)
		}
		return app.Outcome{}, nil
	}

	var done, total int
	if err := tx.QueryRow(ctx, `UPDATE batches SET completed_count = LEAST(completed_count + 1, total) WHERE id = $1
		RETURNING completed_count, total`, batchID).Scan(&done, &total); err != nil {
		return app.Outcome{}, fmt.Errorf("atualizar contador do lote: %w", err)
	}

	out := app.Outcome{}
	if done >= total {
		tag, err := tx.Exec(ctx, `UPDATE batches SET completed_at = now() WHERE id = $1 AND completed_at IS NULL`, batchID)
		if err != nil {
			return app.Outcome{}, fmt.Errorf("fechar o lote: %w", err)
		}
		if tag.RowsAffected() == 1 {
			if err := s.completeBatch(ctx, tx, batchID, total); err != nil {
				return app.Outcome{}, err
			}
			out.BatchCompleted = true
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return app.Outcome{}, fmt.Errorf("commit: %w", err)
	}
	return out, nil
}

func (s Store) completeBatch(ctx context.Context, tx pgx.Tx, batchID string, total int) error {
	var failed int
	if err := tx.QueryRow(ctx, `SELECT count(*) FROM photos WHERE batch_id = $1 AND status = 'failed'`, batchID).Scan(&failed); err != nil {
		return fmt.Errorf("contar falhas do lote: %w", err)
	}
	msg, err := json.Marshal(messages.BatchCompleted{V: messages.Version, MessageID: s.NewID(), BatchID: batchID, Total: total, Failed: failed})
	if err != nil {
		return fmt.Errorf("serializar batch.completed: %w", err)
	}
	if _, err := tx.Exec(ctx, `INSERT INTO outbox (routing_key, payload) VALUES ($1, $2)`, BatchCompletedKey, msg); err != nil {
		return fmt.Errorf("gravar batch.completed no outbox: %w", err)
	}
	evt, _ := json.Marshal(map[string]int{"total": total, "failed": failed})
	if _, err := tx.Exec(ctx, `INSERT INTO events (type, batch_id, payload) VALUES ('batch.completed', $1, $2)`, batchID, evt); err != nil {
		return fmt.Errorf("gravar evento do lote: %w", err)
	}
	return nil
}
