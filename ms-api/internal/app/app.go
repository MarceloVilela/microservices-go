// Package app reúne os tipos do ms-api: o que a leitura devolve e o cursor de paginação.
// As interfaces ficam nos consumidores (httpapi e sse), não aqui.
package app

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"
)

// ErrNotFound é o recurso inexistente.
var ErrNotFound = errors.New("não encontrado")

// Batch é um lote com os contadores. FailedCount vem das fotos `failed`.
type Batch struct {
	ID             string     `json:"id"`
	Total          int        `json:"total"`
	CompletedCount int        `json:"completed_count"`
	FailedCount    int        `json:"failed_count"`
	CreatedAt      time.Time  `json:"created_at"`
	CompletedAt    *time.Time `json:"completed_at"`
}

// Photo é uma foto; FailureClass e FailureReason só existem quando status = failed.
type Photo struct {
	ID            string     `json:"id"`
	BatchID       string     `json:"batch_id"`
	SourceURL     string     `json:"source_url"`
	Status        string     `json:"status"`
	FailureClass  *string    `json:"failure_class"`
	FailureReason *string    `json:"failure_reason"`
	CreatedAt     time.Time  `json:"created_at"`
	FinishedAt    *time.Time `json:"finished_at"`
}

// PhotoFilter são os filtros de GET /photos (já validados).
type PhotoFilter struct {
	Status  string
	BatchID string
}

// Event é uma linha do log de eventos; Seq é o id do evento SSE.
type Event struct {
	Seq       int64           `json:"seq"`
	Type      string          `json:"type"`
	BatchID   *string         `json:"batch_id"`
	PhotoID   *string         `json:"photo_id"`
	Payload   json.RawMessage `json:"payload"`
	CreatedAt time.Time       `json:"created_at"`
}

// RetryResult é o desfecho de POST /dlq/{id}/retry.
type RetryResult int

// Desfechos do retry.
const (
	Reopened  RetryResult = iota // foto failed voltou a pending (outbox e evento gravados)
	NotFound                     // foto inexistente
	NotFailed                    // foto pending ou done: sem efeito
)

var uuidRE = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`)

// ValidUUID confere o formato (minúsculo, 36 caracteres) antes de qualquer consulta ou acesso a arquivo.
func ValidUUID(s string) bool { return uuidRE.MatchString(s) }

// Cursor é a posição numa lista ordenada por (T, ID) decrescente. É opaco para o cliente.
type Cursor struct {
	T  time.Time
	ID string
}

// Encode serializa o cursor (base64url de "<RFC3339Nano>|<id>").
func (c Cursor) Encode() string {
	return base64.RawURLEncoding.EncodeToString([]byte(c.T.UTC().Format(time.RFC3339Nano) + "|" + c.ID))
}

// DecodeCursor é o inverso de Encode; cursor adulterado devolve erro (o handler responde 400).
func DecodeCursor(s string) (Cursor, error) {
	raw, err := base64.RawURLEncoding.DecodeString(s)
	if err != nil {
		return Cursor{}, errors.New("cursor inválido")
	}
	ts, id, ok := strings.Cut(string(raw), "|")
	if !ok || !ValidUUID(id) {
		return Cursor{}, errors.New("cursor inválido")
	}
	t, err := time.Parse(time.RFC3339Nano, ts)
	if err != nil {
		return Cursor{}, fmt.Errorf("cursor inválido: %w", err)
	}
	return Cursor{T: t, ID: id}, nil
}
