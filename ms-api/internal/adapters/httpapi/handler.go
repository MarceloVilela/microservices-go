// Package httpapi é o adapter HTTP de leitura do ms-api: lotes, fotos, miniaturas, DLQ e retry.
// O SSE fica em adapters/sse. Erros em application/problem+json com `code` estável.
package httpapi

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"strconv"
	"time"

	"github.com/MarceloVilela/microservices-go/ms-api/internal/app"
	"github.com/MarceloVilela/microservices-go/platform/httpx"
)

// Limites de paginação.
const (
	DefaultLimit = 20
	MaxLimit     = 100
)

// Reader é o que o handler precisa do armazenamento (interface no consumidor).
// As listas devolvem até limit+1 itens: o excedente indica que há próxima página.
type Reader interface {
	Batches(ctx context.Context, after *app.Cursor, limit int) ([]app.Batch, error)
	Batch(ctx context.Context, id string) (app.Batch, error)
	Photos(ctx context.Context, f app.PhotoFilter, after *app.Cursor, limit int) ([]app.Photo, error)
	// DLQ lista as fotos `failed` por finished_at decrescente.
	DLQ(ctx context.Context, after *app.Cursor, limit int) ([]app.Photo, error)
	Retry(ctx context.Context, photoID string) (app.RetryResult, error)
}

// Thumbs lê uma miniatura; ausente ⇒ app.ErrNotFound.
type Thumbs interface {
	Read(photoID string) ([]byte, time.Time, error)
}

// Handler serve as rotas REST do ms-api.
type Handler struct {
	Reader Reader
	Thumbs Thumbs
	Log    *slog.Logger
}

// Routes são os padrões que o Mount registra (fonte única: o teste de contrato os compara com o OpenAPI).
var Routes = []string{
	"GET /batches", "GET /batches/{id}", "GET /photos", "GET /photos/{id}/thumb", "GET /dlq", "POST /dlq/{id}/retry",
}

// Mount registra as rotas no mux, já envolvidas pelos middlewares (origem, CORS, token).
func (h Handler) Mount(mux *http.ServeMux, mws ...httpx.Middleware) {
	handlers := map[string]http.HandlerFunc{
		"GET /batches": h.listBatches, "GET /batches/{id}": h.getBatch, "GET /photos": h.listPhotos,
		"GET /photos/{id}/thumb": h.thumb, "GET /dlq": h.listDLQ, "POST /dlq/{id}/retry": h.retry,
	}
	for _, pattern := range Routes {
		mux.Handle(pattern, httpx.Chain(handlers[pattern], mws...))
	}
}

func (h Handler) fail(w http.ResponseWriter, r *http.Request, what string, err error) {
	h.Log.Error(what, "erro", err, "path", r.URL.Path)
	httpx.Problem(w, http.StatusInternalServerError, "internal_error", "erro interno")
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

// pageParams lê `cursor` e `limit`; responde 400 e devolve ok=false se inválidos.
func pageParams(w http.ResponseWriter, r *http.Request) (after *app.Cursor, limit int, ok bool) {
	limit = DefaultLimit
	if s := r.URL.Query().Get("limit"); s != "" {
		n, err := strconv.Atoi(s)
		if err != nil || n < 1 || n > MaxLimit {
			httpx.Problem(w, http.StatusBadRequest, "invalid_limit", "limit deve ser um inteiro de 1 a "+strconv.Itoa(MaxLimit))
			return nil, 0, false
		}
		limit = n
	}
	if s := r.URL.Query().Get("cursor"); s != "" {
		c, err := app.DecodeCursor(s)
		if err != nil {
			httpx.Problem(w, http.StatusBadRequest, "invalid_cursor", "cursor inválido")
			return nil, 0, false
		}
		after = &c
	}
	return after, limit, true
}

type page[T any] struct {
	Items      []T     `json:"items"`
	NextCursor *string `json:"next_cursor"`
}

// paginate corta o item excedente e monta o cursor da próxima página a partir do último item mantido.
func paginate[T any](items []T, limit int, cursorOf func(T) app.Cursor) page[T] {
	p := page[T]{Items: items}
	if len(items) > limit {
		p.Items = items[:limit]
		c := cursorOf(p.Items[limit-1]).Encode()
		p.NextCursor = &c
	}
	if p.Items == nil {
		p.Items = []T{}
	}
	return p
}

func (h Handler) listBatches(w http.ResponseWriter, r *http.Request) {
	after, limit, ok := pageParams(w, r)
	if !ok {
		return
	}
	items, err := h.Reader.Batches(r.Context(), after, limit+1)
	if err != nil {
		h.fail(w, r, "listar lotes", err)
		return
	}
	writeJSON(w, http.StatusOK, paginate(items, limit, func(b app.Batch) app.Cursor { return app.Cursor{T: b.CreatedAt, ID: b.ID} }))
}

func (h Handler) getBatch(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if !app.ValidUUID(id) {
		httpx.Problem(w, http.StatusNotFound, "batch_not_found", "lote não encontrado")
		return
	}
	b, err := h.Reader.Batch(r.Context(), id)
	switch {
	case errors.Is(err, app.ErrNotFound):
		httpx.Problem(w, http.StatusNotFound, "batch_not_found", "lote não encontrado")
	case err != nil:
		h.fail(w, r, "ler lote", err)
	default:
		writeJSON(w, http.StatusOK, b)
	}
}

func (h Handler) listPhotos(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	f := app.PhotoFilter{Status: q.Get("status"), BatchID: q.Get("batch_id")}
	if f.Status != "" && f.Status != "pending" && f.Status != "done" && f.Status != "failed" {
		httpx.Problem(w, http.StatusBadRequest, "invalid_status", "status deve ser pending, done ou failed")
		return
	}
	if f.BatchID != "" && !app.ValidUUID(f.BatchID) {
		httpx.Problem(w, http.StatusBadRequest, "invalid_batch_id", "batch_id deve ser um UUID")
		return
	}
	after, limit, ok := pageParams(w, r)
	if !ok {
		return
	}
	items, err := h.Reader.Photos(r.Context(), f, after, limit+1)
	if err != nil {
		h.fail(w, r, "listar fotos", err)
		return
	}
	writeJSON(w, http.StatusOK, paginate(items, limit, func(p app.Photo) app.Cursor { return app.Cursor{T: p.CreatedAt, ID: p.ID} }))
}

func (h Handler) listDLQ(w http.ResponseWriter, r *http.Request) {
	after, limit, ok := pageParams(w, r)
	if !ok {
		return
	}
	items, err := h.Reader.DLQ(r.Context(), after, limit+1)
	if err != nil {
		h.fail(w, r, "listar a DLQ", err)
		return
	}
	writeJSON(w, http.StatusOK, paginate(items, limit, func(p app.Photo) app.Cursor {
		t := p.CreatedAt
		if p.FinishedAt != nil {
			t = *p.FinishedAt
		}
		return app.Cursor{T: t, ID: p.ID}
	}))
}

func (h Handler) retry(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if !app.ValidUUID(id) {
		httpx.Problem(w, http.StatusNotFound, "photo_not_found", "foto não encontrada")
		return
	}
	res, err := h.Reader.Retry(r.Context(), id)
	switch {
	case err != nil:
		h.fail(w, r, "reprocessar foto", err)
	case res == app.NotFound:
		httpx.Problem(w, http.StatusNotFound, "photo_not_found", "foto não encontrada")
	case res == app.NotFailed:
		httpx.Problem(w, http.StatusConflict, "photo_not_failed", "só foto com status failed pode ser reprocessada")
	default:
		writeJSON(w, http.StatusAccepted, map[string]string{"photo_id": id, "status": "pending"})
	}
}

func (h Handler) thumb(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	b, mod, err := h.Thumbs.Read(id)
	switch {
	case errors.Is(err, app.ErrNotFound):
		httpx.Problem(w, http.StatusNotFound, "thumb_not_found", "miniatura não encontrada")
		return
	case err != nil:
		h.fail(w, r, "ler miniatura", err)
		return
	}
	sum := sha256.Sum256(b)
	w.Header().Set("ETag", `"`+hex.EncodeToString(sum[:16])+`"`)
	w.Header().Set("Content-Type", "image/jpeg")
	w.Header().Set("Cache-Control", "private, max-age=0, must-revalidate")
	// ServeContent trata If-None-Match (304) e Range
	http.ServeContent(w, r, "", mod, bytes.NewReader(b))
}
