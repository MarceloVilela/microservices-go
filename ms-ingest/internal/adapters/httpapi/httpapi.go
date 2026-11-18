// Package httpapi é o adapter HTTP do ms-ingest (POST /batches).
package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"mime"
	"net/http"

	"github.com/MarceloVilela/microservices-go/ms-ingest/internal/app"
	"github.com/MarceloVilela/microservices-go/ms-ingest/internal/domain"
	"github.com/MarceloVilela/microservices-go/platform/httpx"
)

// MaxBody é o limite do corpo do POST /batches.
const MaxBody = 1 << 20

// Creator é o que o handler precisa do caso de uso (interface no consumidor).
type Creator interface {
	CreateBatch(ctx context.Context, key string, req domain.Request) (app.Stored, error)
}

// Handler serve POST /batches.
type Handler struct {
	Creator Creator
	Log     *slog.Logger
}

type response struct {
	BatchID string `json:"batch_id"`
	Total   int    `json:"total"`
	Status  string `json:"status"`
}

// ServeHTTP implementa POST /batches.
func (h Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if mt, _, err := mime.ParseMediaType(r.Header.Get("Content-Type")); err != nil || mt != "application/json" {
		httpx.Problem(w, http.StatusUnsupportedMediaType, "unsupported_media_type", "Content-Type deve ser application/json")
		return
	}
	body, err := httpx.ReadBody(w, r, MaxBody)
	if err != nil {
		status := httpx.StatusForBodyError(err)
		code := "invalid_request"
		if status == http.StatusRequestEntityTooLarge {
			code = "body_too_large"
		}
		httpx.Problem(w, status, code, "corpo ilegível ou acima de 1 MiB")
		return
	}
	var req domain.Request
	dec := json.NewDecoder(bytes.NewReader(body))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&req); err != nil || dec.More() {
		httpx.Problem(w, http.StatusBadRequest, "invalid_request", "JSON inválido; esperado {\"photos\":[{\"source_url\":\"...\"}]}")
		return
	}

	res, err := h.Creator.CreateBatch(r.Context(), r.Header.Get("Idempotency-Key"), req)
	var ve *domain.ValidationError
	switch {
	case errors.As(err, &ve):
		httpx.Problem(w, http.StatusBadRequest, "invalid_request", ve.Msg)
		return
	case errors.Is(err, app.ErrKeyReuse):
		httpx.Problem(w, http.StatusUnprocessableEntity, "idempotency_key_reuse", "a Idempotency-Key já foi usada com outro corpo")
		return
	case err != nil:
		h.Log.Error("criar lote", "erro", err)
		httpx.Problem(w, http.StatusInternalServerError, "internal_error", "erro interno")
		return
	}

	status := http.StatusAccepted
	if res.Replay {
		status = http.StatusOK
		w.Header().Set("Idempotent-Replay", "true")
	}
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Location", "/batches/"+res.ID)
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(response{BatchID: res.ID, Total: res.Total, Status: "accepted"})
}
