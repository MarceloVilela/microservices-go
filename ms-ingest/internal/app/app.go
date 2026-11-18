// Package app é o caso de uso "criar lote". A persistência é uma interface
// definida aqui, no consumidor (ADR 0002, D6).
package app

import (
	"context"
	"errors"

	"github.com/MarceloVilela/microservices-go/ms-ingest/internal/domain"
)

// ErrKeyReuse: a mesma Idempotency-Key chegou com um corpo diferente.
var ErrKeyReuse = errors.New("idempotency key já usada com outro corpo")

// NewBatch é o que o Store grava, tudo numa só transação: lote, fotos, uma linha
// de outbox por foto e o evento batch.created.
type NewBatch struct {
	ID             string
	IdempotencyKey string // vazia = sem idempotência
	RequestHash    string
	Photos         []NewPhoto
}

// NewPhoto é uma foto do lote.
type NewPhoto struct {
	ID        string
	SourceURL string
}

// Stored é o lote como ficou gravado.
type Stored struct {
	ID     string
	Total  int
	Replay bool // true: a chave já existia e este é o lote original
}

// Store persiste o lote. Com chave repetida e mesmo hash devolve o lote
// original (Replay); com hash diferente devolve ErrKeyReuse.
type Store interface {
	CreateBatch(ctx context.Context, b NewBatch) (Stored, error)
}

// Service cria lotes.
type Service struct {
	Store   Store
	Allowed []string // hosts permitidos, minúsculos
}

// CreateBatch valida **antes** de tocar no banco e grava o lote.
// key vazia desliga a idempotência.
func (s Service) CreateBatch(ctx context.Context, key string, req domain.Request) (Stored, error) {
	if key != "" && !domain.ValidKey(key) {
		return Stored{}, &domain.ValidationError{Msg: "Idempotency-Key deve ter de 1 a 128 caracteres de A-Z a-z 0-9 . _ -"}
	}
	if err := req.Validate(s.Allowed); err != nil {
		return Stored{}, err
	}
	b := NewBatch{ID: domain.NewID(), IdempotencyKey: key, RequestHash: req.Hash()}
	for _, p := range req.Photos {
		b.Photos = append(b.Photos, NewPhoto{ID: domain.NewID(), SourceURL: p.SourceURL})
	}
	return s.Store.CreateBatch(ctx, b)
}
