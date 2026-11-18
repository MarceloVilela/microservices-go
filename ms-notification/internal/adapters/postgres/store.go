// Package postgres guarda a deduplicação por batch_id (tabela notifications, migração 0003).
package postgres

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5/pgxpool"
)

// Store implementa app.Store.
type Store struct{ Pool *pgxpool.Pool }

// Once roda fn dentro de uma transação que segura um advisory lock do batch_id: entregas
// concorrentes do mesmo lote esperam aqui, e a segunda já encontra a linha gravada. O lock
// vale até o commit, então o banco fica ocupado durante fn (limitado pelo timeout dos destinos).
func (s Store) Once(ctx context.Context, batchID string, fn func(ctx context.Context) string) (bool, error) {
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return false, fmt.Errorf("abrir transação: %w", err)
	}
	// rollback com contexto próprio: precisa rodar mesmo com ctx cancelado
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()

	if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1, 0))`, batchID); err != nil {
		return false, fmt.Errorf("advisory lock: %w", err)
	}
	var seen bool
	if err := tx.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM notifications WHERE batch_id = $1)`, batchID).Scan(&seen); err != nil {
		return false, fmt.Errorf("consultar notifications: %w", err)
	}
	if seen {
		return false, nil
	}
	lastErr := fn(ctx)
	var le *string
	if lastErr != "" {
		le = &lastErr
	}
	if _, err := tx.Exec(ctx, `INSERT INTO notifications (batch_id, last_error) VALUES ($1, $2)`, batchID, le); err != nil {
		return false, fmt.Errorf("gravar notifications: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return false, fmt.Errorf("commit: %w", err)
	}
	return true, nil
}
