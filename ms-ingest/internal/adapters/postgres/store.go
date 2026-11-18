// Package postgres grava o lote, as fotos, o outbox e o evento numa só transação.
package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/MarceloVilela/microservices-go/ms-ingest/internal/app"
	"github.com/MarceloVilela/microservices-go/ms-ingest/internal/domain"
	"github.com/MarceloVilela/microservices-go/platform/messages"
)

// Store implementa app.Store.
type Store struct{ Pool *pgxpool.Pool }

// ProcessKey é a chave de roteamento das mensagens photo.process.
const ProcessKey = "process"

// CreateBatch grava tudo ou nada. Com Idempotency-Key, o INSERT ... ON CONFLICT
// DO NOTHING serializa as requisições concorrentes: a segunda espera o commit da
// primeira e então lê o lote dela.
func (s Store) CreateBatch(ctx context.Context, b app.NewBatch) (app.Stored, error) {
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return app.Stored{}, fmt.Errorf("abrir transação: %w", err)
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()

	var key, hash *string
	if b.IdempotencyKey != "" {
		key, hash = &b.IdempotencyKey, &b.RequestHash
	}
	tag, err := tx.Exec(ctx, `INSERT INTO batches (id, idempotency_key, request_hash, total)
		VALUES ($1, $2, $3, $4) ON CONFLICT (idempotency_key) DO NOTHING`, b.ID, key, hash, len(b.Photos))
	if err != nil {
		return app.Stored{}, fmt.Errorf("inserir lote: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return replay(ctx, tx, b)
	}

	batch := &pgx.Batch{}
	for _, p := range b.Photos {
		batch.Queue(`INSERT INTO photos (id, batch_id, source_url) VALUES ($1, $2, $3)`, p.ID, b.ID, p.SourceURL)
		payload, err := json.Marshal(messages.PhotoProcess{
			V: messages.Version, MessageID: domain.NewID(), BatchID: b.ID, PhotoID: p.ID, SourceURL: p.SourceURL,
		})
		if err != nil {
			return app.Stored{}, fmt.Errorf("serializar mensagem: %w", err)
		}
		batch.Queue(`INSERT INTO outbox (routing_key, payload) VALUES ($1, $2)`, ProcessKey, payload)
	}
	eventPayload, _ := json.Marshal(map[string]int{"total": len(b.Photos)})
	batch.Queue(`INSERT INTO events (type, batch_id, payload) VALUES ('batch.created', $1, $2)`, b.ID, eventPayload)

	res := tx.SendBatch(ctx, batch)
	for range batch.Len() {
		if _, err := res.Exec(); err != nil {
			_ = res.Close()
			return app.Stored{}, fmt.Errorf("gravar fotos, outbox e evento: %w", err)
		}
	}
	if err := res.Close(); err != nil {
		return app.Stored{}, fmt.Errorf("gravar fotos, outbox e evento: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return app.Stored{}, fmt.Errorf("commit: %w", err)
	}
	return app.Stored{ID: b.ID, Total: len(b.Photos)}, nil
}

// replay devolve o lote que já tem a chave, ou ErrKeyReuse se o corpo difere.
func replay(ctx context.Context, tx pgx.Tx, b app.NewBatch) (app.Stored, error) {
	var id, hash string
	var total int
	err := tx.QueryRow(ctx, `SELECT id, request_hash, total FROM batches WHERE idempotency_key = $1`, b.IdempotencyKey).Scan(&id, &hash, &total)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return app.Stored{}, errors.New("conflito de chave sem lote correspondente")
		}
		return app.Stored{}, fmt.Errorf("ler lote da chave: %w", err)
	}
	if hash != b.RequestHash {
		return app.Stored{}, app.ErrKeyReuse
	}
	return app.Stored{ID: id, Total: total, Replay: true}, nil
}
