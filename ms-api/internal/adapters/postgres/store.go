// Package postgres implementa a leitura do ms-api (lotes, fotos, DLQ, log de eventos) e o retry.
// SQL sempre parametrizado; o ms-api não migra o esquema (o ms-ingest migra; ADR 0007).
package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/MarceloVilela/microservices-go/ms-api/internal/app"
	"github.com/MarceloVilela/microservices-go/platform/messages"
)

// ProcessKey é a chave de roteamento do photo.process (a mesma que o ms-ingest grava no outbox).
const ProcessKey = "process"

// Store lê e escreve no Postgres.
type Store struct {
	Pool *pgxpool.Pool
}

// cursorArgs devolve (t, id) para o predicado `(col, id) < (t, id)`; nil ⇒ primeira página.
func cursorArgs(after *app.Cursor) (*time.Time, *string) {
	if after == nil {
		return nil, nil
	}
	return &after.T, &after.ID
}

// Batches lista por created_at desc, id desc.
func (s Store) Batches(ctx context.Context, after *app.Cursor, limit int) ([]app.Batch, error) {
	t, id := cursorArgs(after)
	rows, err := s.Pool.Query(ctx, `
		SELECT b.id, b.total, b.completed_count,
		       (SELECT count(*) FROM photos p WHERE p.batch_id = b.id AND p.status = 'failed'),
		       b.created_at, b.completed_at
		FROM batches b
		WHERE $1::timestamptz IS NULL OR (b.created_at, b.id) < ($1::timestamptz, $2::uuid)
		ORDER BY b.created_at DESC, b.id DESC
		LIMIT $3`, t, id, limit)
	if err != nil {
		return nil, fmt.Errorf("listar lotes: %w", err)
	}
	defer rows.Close()
	var out []app.Batch
	for rows.Next() {
		var b app.Batch
		if err := rows.Scan(&b.ID, &b.Total, &b.CompletedCount, &b.FailedCount, &b.CreatedAt, &b.CompletedAt); err != nil {
			return nil, fmt.Errorf("ler lote: %w", err)
		}
		out = append(out, b)
	}
	return out, rows.Err()
}

// Batch devolve um lote ou app.ErrNotFound.
func (s Store) Batch(ctx context.Context, id string) (app.Batch, error) {
	var b app.Batch
	err := s.Pool.QueryRow(ctx, `
		SELECT b.id, b.total, b.completed_count,
		       (SELECT count(*) FROM photos p WHERE p.batch_id = b.id AND p.status = 'failed'),
		       b.created_at, b.completed_at
		FROM batches b WHERE b.id = $1`, id).
		Scan(&b.ID, &b.Total, &b.CompletedCount, &b.FailedCount, &b.CreatedAt, &b.CompletedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return app.Batch{}, app.ErrNotFound
	}
	if err != nil {
		return app.Batch{}, fmt.Errorf("ler lote: %w", err)
	}
	return b, nil
}

const photoCols = `p.id, p.batch_id, p.source_url, p.status, p.failure_class, p.failure_reason, p.created_at, p.finished_at`

func scanPhotos(rows pgx.Rows) ([]app.Photo, error) {
	defer rows.Close()
	var out []app.Photo
	for rows.Next() {
		var p app.Photo
		if err := rows.Scan(&p.ID, &p.BatchID, &p.SourceURL, &p.Status, &p.FailureClass, &p.FailureReason, &p.CreatedAt, &p.FinishedAt); err != nil {
			return nil, fmt.Errorf("ler foto: %w", err)
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

// Photos lista por created_at desc, id desc, com filtros opcionais (string vazia = sem filtro).
func (s Store) Photos(ctx context.Context, f app.PhotoFilter, after *app.Cursor, limit int) ([]app.Photo, error) {
	t, id := cursorArgs(after)
	rows, err := s.Pool.Query(ctx, `
		SELECT `+photoCols+` FROM photos p
		WHERE ($1 = '' OR p.status = $1)
		  AND ($2 = '' OR p.batch_id = NULLIF($2, '')::uuid)
		  AND ($3::timestamptz IS NULL OR (p.created_at, p.id) < ($3::timestamptz, $4::uuid))
		ORDER BY p.created_at DESC, p.id DESC
		LIMIT $5`, f.Status, f.BatchID, t, id, limit)
	if err != nil {
		return nil, fmt.Errorf("listar fotos: %w", err)
	}
	return scanPhotos(rows)
}

// DLQ lista as fotos `failed` por finished_at desc, id desc (a causa vem em failure_*).
func (s Store) DLQ(ctx context.Context, after *app.Cursor, limit int) ([]app.Photo, error) {
	t, id := cursorArgs(after)
	rows, err := s.Pool.Query(ctx, `
		SELECT `+photoCols+` FROM photos p
		WHERE p.status = 'failed'
		  AND ($1::timestamptz IS NULL OR (p.finished_at, p.id) < ($1::timestamptz, $2::uuid))
		ORDER BY p.finished_at DESC, p.id DESC
		LIMIT $3`, t, id, limit)
	if err != nil {
		return nil, fmt.Errorf("listar a DLQ: %w", err)
	}
	return scanPhotos(rows)
}

// Retry reabre uma foto `failed` (ADR 0018): volta a `pending`, grava o photo.process no outbox e o
// evento `photo.retried`, tudo na mesma transação. O lote não é tocado (completed_count e
// batch.completed ficam como estão; o worker só conta o lote na 1ª finalização da foto).
func (s Store) Retry(ctx context.Context, photoID string) (app.RetryResult, error) {
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return 0, fmt.Errorf("abrir transação: %w", err)
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()

	var batchID, sourceURL string
	err = tx.QueryRow(ctx, `
		UPDATE photos SET status = 'pending', failure_class = NULL, failure_reason = NULL, finished_at = NULL
		WHERE id = $1 AND status = 'failed' RETURNING batch_id, source_url`, photoID).Scan(&batchID, &sourceURL)
	if errors.Is(err, pgx.ErrNoRows) {
		var exists bool
		if err := tx.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM photos WHERE id = $1)`, photoID).Scan(&exists); err != nil {
			return 0, fmt.Errorf("conferir foto: %w", err)
		}
		if !exists {
			return app.NotFound, nil
		}
		return app.NotFailed, nil
	}
	if err != nil {
		return 0, fmt.Errorf("reabrir foto: %w", err)
	}

	payload, err := json.Marshal(messages.PhotoProcess{
		V: messages.Version, MessageID: messages.NewID(), BatchID: batchID, PhotoID: photoID, SourceURL: sourceURL,
	})
	if err != nil {
		return 0, fmt.Errorf("serializar mensagem: %w", err)
	}
	if _, err := tx.Exec(ctx, `INSERT INTO outbox (routing_key, payload) VALUES ($1, $2)`, ProcessKey, payload); err != nil {
		return 0, fmt.Errorf("gravar no outbox: %w", err)
	}
	if _, err := tx.Exec(ctx, `INSERT INTO events (type, batch_id, photo_id, payload) VALUES ('photo.retried', $1, $2, '{}')`, batchID, photoID); err != nil {
		return 0, fmt.Errorf("gravar evento: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return 0, fmt.Errorf("commit: %w", err)
	}
	return app.Reopened, nil
}

// Bounds devolve o menor e o maior seq retidos; (0, 0) com o log vazio.
func (s Store) Bounds(ctx context.Context) (int64, int64, error) {
	var lo, hi *int64
	if err := s.Pool.QueryRow(ctx, `SELECT min(seq), max(seq) FROM events`).Scan(&lo, &hi); err != nil {
		return 0, 0, fmt.Errorf("limites do log de eventos: %w", err)
	}
	if lo == nil || hi == nil {
		// log vazio (tudo podado): a sequência já avançou, e o próximo evento não é "buraco" em relação a 0
		var last *int64
		if err := s.Pool.QueryRow(ctx, `SELECT pg_sequence_last_value(pg_get_serial_sequence('events', 'seq')::regclass)`).Scan(&last); err != nil {
			return 0, 0, fmt.Errorf("última seq usada: %w", err)
		}
		if last == nil {
			return 0, 0, nil
		}
		return 0, *last, nil
	}
	return *lo, *hi, nil
}

// After devolve até limit eventos com seq > after, em ordem crescente.
func (s Store) After(ctx context.Context, after int64, limit int) ([]app.Event, error) {
	rows, err := s.Pool.Query(ctx, `
		SELECT seq, type, batch_id::text, photo_id::text, payload, created_at
		FROM events WHERE seq > $1 ORDER BY seq LIMIT $2`, after, limit)
	if err != nil {
		return nil, fmt.Errorf("ler eventos: %w", err)
	}
	defer rows.Close()
	var out []app.Event
	for rows.Next() {
		var e app.Event
		if err := rows.Scan(&e.Seq, &e.Type, &e.BatchID, &e.PhotoID, &e.Payload, &e.CreatedAt); err != nil {
			return nil, fmt.Errorf("ler evento: %w", err)
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

// PruneEvents apaga eventos mais velhos que a retenção e devolve quantos.
func (s Store) PruneEvents(ctx context.Context, olderThan time.Duration) (int64, error) {
	tag, err := s.Pool.Exec(ctx, `DELETE FROM events WHERE created_at < now() - make_interval(secs => $1)`, olderThan.Seconds())
	if err != nil {
		return 0, fmt.Errorf("podar eventos: %w", err)
	}
	return tag.RowsAffected(), nil
}
