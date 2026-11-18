package httpapi_test

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/MarceloVilela/microservices-go/ms-ingest/internal/adapters/httpapi"
	"github.com/MarceloVilela/microservices-go/ms-ingest/internal/app"
	"github.com/MarceloVilela/microservices-go/ms-ingest/internal/domain"
)

type fake struct {
	res  app.Stored
	err  error
	key  string
	seen int
}

func (f *fake) CreateBatch(_ context.Context, key string, req domain.Request) (app.Stored, error) {
	f.seen++
	f.key = key
	return f.res, f.err
}

func post(f *fake, ct, body string, hdr ...string) *httptest.ResponseRecorder {
	h := httpapi.Handler{Creator: f, Log: slog.New(slog.DiscardHandler)}
	req := httptest.NewRequest(http.MethodPost, "/batches", strings.NewReader(body))
	if ct != "" {
		req.Header.Set("Content-Type", ct)
	}
	for i := 0; i+1 < len(hdr); i += 2 {
		req.Header.Set(hdr[i], hdr[i+1])
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

const okBody = `{"photos":[{"source_url":"https://picsum.photos/id/1/1/1"}]}`

func code(t *testing.T, rec *httptest.ResponseRecorder) string {
	t.Helper()
	var p map[string]any
	_ = json.Unmarshal(rec.Body.Bytes(), &p)
	s, _ := p["code"].(string)
	return s
}

func TestAceito202(t *testing.T) {
	f := &fake{res: app.Stored{ID: "abc", Total: 1}}
	rec := post(f, "application/json", okBody, "Idempotency-Key", "k1")
	if rec.Code != 202 || f.key != "k1" || rec.Header().Get("Location") != "/batches/abc" {
		t.Fatalf("code=%d key=%q loc=%q", rec.Code, f.key, rec.Header().Get("Location"))
	}
	var r map[string]any
	_ = json.Unmarshal(rec.Body.Bytes(), &r)
	if r["batch_id"] != "abc" || r["total"] != float64(1) || r["status"] != "accepted" {
		t.Errorf("corpo = %v", r)
	}
}

func TestRepeticaDevolve200(t *testing.T) {
	rec := post(&fake{res: app.Stored{ID: "abc", Total: 1, Replay: true}}, "application/json; charset=utf-8", okBody)
	if rec.Code != 200 || rec.Header().Get("Idempotent-Replay") != "true" {
		t.Errorf("code=%d hdr=%v", rec.Code, rec.Header())
	}
}

func TestErros(t *testing.T) {
	cases := []struct {
		name, ct, body string
		err            error
		status         int
		code           string
		touches        bool
	}{
		{"sem content-type", "", okBody, nil, 415, "unsupported_media_type", false},
		{"content-type errado", "text/plain", okBody, nil, 415, "unsupported_media_type", false},
		{"json malformado", "application/json", `{"photos":`, nil, 400, "invalid_request", false},
		{"campo desconhecido", "application/json", `{"photos":[],"x":1}`, nil, 400, "invalid_request", false},
		{"lixo depois do json", "application/json", okBody + `{}`, nil, 400, "invalid_request", false},
		{"corpo grande", "application/json", `{"photos":[{"source_url":"` + strings.Repeat("a", httpapi.MaxBody) + `"}]}`, nil, 413, "body_too_large", false},
		{"validação", "application/json", okBody, &domain.ValidationError{Msg: "x"}, 400, "invalid_request", true},
		{"reuso de chave", "application/json", okBody, app.ErrKeyReuse, 422, "idempotency_key_reuse", true},
		{"erro interno", "application/json", okBody, errors.New("pg: senha=segredo"), 500, "internal_error", true},
	}
	for _, c := range cases {
		f := &fake{err: c.err}
		rec := post(f, c.ct, c.body)
		if rec.Code != c.status || code(t, rec) != c.code || (f.seen > 0) != c.touches {
			t.Errorf("%s: status=%d code=%q seen=%d", c.name, rec.Code, code(t, rec), f.seen)
		}
		if strings.Contains(rec.Body.String(), "segredo") {
			t.Errorf("%s: vazou detalhe interno", c.name)
		}
	}
}
