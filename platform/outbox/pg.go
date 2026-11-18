package outbox

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// PgStore implementa Store sobre o Postgres.
type PgStore struct{ Pool *pgxpool.Pool }

// Begin abre a transação do lote.
func (s PgStore) Begin(ctx context.Context) (Tx, error) {
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	return pgTx{tx}, nil
}

type pgTx struct{ tx pgx.Tx }

// Pending trava até limit linhas pendentes, na ordem do id. SKIP LOCKED faz
// duas instâncias do relay dividirem o trabalho sem esperar uma pela outra.
func (t pgTx) Pending(ctx context.Context, limit int) ([]Message, error) {
	rows, err := t.tx.Query(ctx, `SELECT id, routing_key, payload::text FROM outbox
		WHERE published_at IS NULL ORDER BY id LIMIT $1 FOR UPDATE SKIP LOCKED`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Message
	for rows.Next() {
		var m Message
		var payload string
		if err := rows.Scan(&m.ID, &m.RoutingKey, &payload); err != nil {
			return nil, fmt.Errorf("scan: %w", err)
		}
		m.Payload = []byte(payload)
		out = append(out, m)
	}
	return out, rows.Err()
}

func (t pgTx) MarkPublished(ctx context.Context, ids []int64) error {
	_, err := t.tx.Exec(ctx, "UPDATE outbox SET published_at = now() WHERE id = ANY($1)", ids)
	return err
}

func (t pgTx) Commit(ctx context.Context) error   { return t.tx.Commit(ctx) }
func (t pgTx) Rollback(ctx context.Context) error { return t.tx.Rollback(ctx) }
