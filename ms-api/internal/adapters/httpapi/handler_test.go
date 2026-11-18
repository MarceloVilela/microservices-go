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
	"time"

	"github.com/MarceloVilela/microservices-go/ms-api/internal/adapters/httpapi"
	"github.com/MarceloVilela/microservices-go/ms-api/internal/app"
	"github.com/MarceloVilela/microservices-go/platform/httpx"
)

const (
	id1 = "0192f0c0-1111-7222-8333-444455556661"
	id2 = "0192f0c0-1111-7222-8333-444455556662"
	id3 = "0192f0c0-1111-7222-8333-444455556663"
)

type fakeReader struct {
	batches []app.Batch
	photos  []app.Photo
	retry   app.RetryResult
	err     error

	gotLimit  int
	gotAfter  *app.Cursor
	gotFilter app.PhotoFilter
	retried   string
}

func (f *fakeReader) Batches(_ context.Context, after *app.Cursor, limit int) ([]app.Batch, error) {
	f.gotAfter, f.gotLimit = after, limit
	return f.batches, f.err
}

func (f *fakeReader) Batch(_ context.Context, id string) (app.Batch, error) {
	for _, b := range f.batches {
		if b.ID == id {
			return b, nil
		}
	}
	if f.err != nil {
		return app.Batch{}, f.err
	}
	return app.Batch{}, app.ErrNotFound
}

func (f *fakeReader) Photos(_ context.Context, fl app.PhotoFilter, after *app.Cursor, limit int) ([]app.Photo, error) {
	f.gotAfter, f.gotLimit, f.gotFilter = after, limit, fl
	return f.photos, f.err
}

func (f *fakeReader) DLQ(_ context.Context, after *app.Cursor, limit int) ([]app.Photo, error) {
	f.gotAfter, f.gotLimit = after, limit
	return f.photos, f.err
}

func (f *fakeReader) Retry(_ context.Context, id string) (app.RetryResult, error) {
	f.retried = id
	return f.retry, f.err
}

type fakeThumbs struct {
	body []byte
	err  error
}

func (f fakeThumbs) Read(string) ([]byte, time.Time, error) {
	return f.body, time.Date(2026, 11, 18, 0, 0, 0, 0, time.UTC), f.err
}

func server(r *fakeReader, th fakeThumbs) http.Handler {
	mux := http.NewServeMux()
	httpapi.Handler{Reader: r, Thumbs: th, Log: slog.New(slog.DiscardHandler)}.Mount(mux, httpx.Token("segredo"))
	return mux
}

func call(h http.Handler, method, target string, hdr ...string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, target, nil)
	req.Header.Set("Authorization", "Bearer segredo")
	for i := 0; i+1 < len(hdr); i += 2 {
		req.Header.Set(hdr[i], hdr[i+1])
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

func code(t *testing.T, rec *httptest.ResponseRecorder) string {
	t.Helper()
	if ct := rec.Header().Get("Content-Type"); ct != "application/problem+json" {
		t.Fatalf("Content-Type = %q, quer problem+json (corpo %s)", ct, rec.Body)
	}
	var p map[string]any
	_ = json.Unmarshal(rec.Body.Bytes(), &p)
	c, _ := p["code"].(string)
	return c
}

func TestTodasAsRotasExigemToken(t *testing.T) {
	h := server(&fakeReader{}, fakeThumbs{})
	for _, route := range []struct{ method, path string }{
		{"GET", "/batches"}, {"GET", "/batches/" + id1}, {"GET", "/photos"},
		{"GET", "/photos/" + id1 + "/thumb"}, {"GET", "/dlq"}, {"POST", "/dlq/" + id1 + "/retry"},
	} {
		req := httptest.NewRequest(route.method, route.path, nil) // sem Authorization
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		if rec.Code != 401 || code(t, rec) != "unauthorized" {
			t.Errorf("%s %s sem token: %d", route.method, route.path, rec.Code)
		}
	}
}

func TestPaginacaoCortaOExcedenteEMontaOCursor(t *testing.T) {
	t0 := time.Date(2026, 11, 18, 10, 0, 0, 0, time.UTC)
	r := &fakeReader{batches: []app.Batch{
		{ID: id1, CreatedAt: t0.Add(2 * time.Second)}, {ID: id2, CreatedAt: t0.Add(time.Second)}, {ID: id3, CreatedAt: t0},
	}}
	h := server(r, fakeThumbs{})

	rec := call(h, "GET", "/batches?limit=2")
	if rec.Code != 200 || r.gotLimit != 3 { // o store recebe limit+1
		t.Fatalf("status %d, limit repassado %d (quer 3)", rec.Code, r.gotLimit)
	}
	var p struct {
		Items      []app.Batch `json:"items"`
		NextCursor *string     `json:"next_cursor"`
	}
	_ = json.Unmarshal(rec.Body.Bytes(), &p)
	if len(p.Items) != 2 || p.NextCursor == nil {
		t.Fatalf("items=%d next=%v", len(p.Items), p.NextCursor)
	}
	c, err := app.DecodeCursor(*p.NextCursor)
	if err != nil || c.ID != id2 || !c.T.Equal(t0.Add(time.Second)) { // cursor = último item MANTIDO
		t.Errorf("cursor = %+v, %v; quer o do 2º item", c, err)
	}

	// próxima página: o cursor volta ao store
	call(h, "GET", "/batches?limit=2&cursor="+*p.NextCursor)
	if r.gotAfter == nil || r.gotAfter.ID != id2 {
		t.Errorf("cursor não chegou ao store: %+v", r.gotAfter)
	}

	// última página: sem excedente, sem next_cursor, lista vazia vira [] (não null)
	r.batches = nil
	rec = call(h, "GET", "/batches")
	if !strings.Contains(rec.Body.String(), `"items":[]`) || !strings.Contains(rec.Body.String(), `"next_cursor":null`) {
		t.Errorf("corpo = %s", rec.Body)
	}
}

func TestParametrosInvalidos(t *testing.T) {
	h := server(&fakeReader{}, fakeThumbs{})
	for target, want := range map[string]string{
		"/batches?limit=0":           "invalid_limit",
		"/batches?limit=101":         "invalid_limit",
		"/batches?limit=abc":         "invalid_limit",
		"/batches?cursor=adulterado": "invalid_cursor",
		"/photos?status=quebrada":    "invalid_status",
		"/photos?batch_id=nope":      "invalid_batch_id",
		"/dlq?cursor=%00":            "invalid_cursor",
	} {
		rec := call(h, "GET", target)
		if rec.Code != 400 || code(t, rec) != want {
			t.Errorf("%s: %d %s, quer 400 %s", target, rec.Code, rec.Body, want)
		}
	}
}

func TestFiltrosDeFotosChegamAoStore(t *testing.T) {
	r := &fakeReader{}
	call(server(r, fakeThumbs{}), "GET", "/photos?status=failed&batch_id="+id1)
	if r.gotFilter.Status != "failed" || r.gotFilter.BatchID != id1 {
		t.Errorf("filtro = %+v", r.gotFilter)
	}
}

func TestLoteInexistenteOuIdMalformado(t *testing.T) {
	h := server(&fakeReader{}, fakeThumbs{})
	for _, path := range []string{"/batches/" + id1, "/batches/nao-e-uuid", "/batches/..%2f..%2fx"} {
		rec := call(h, "GET", path)
		if rec.Code != 404 || code(t, rec) != "batch_not_found" {
			t.Errorf("%s: %d", path, rec.Code)
		}
	}
	rec := call(server(&fakeReader{batches: []app.Batch{{ID: id1, Total: 3}}}, fakeThumbs{}), "GET", "/batches/"+id1)
	if rec.Code != 200 || !strings.Contains(rec.Body.String(), `"total":3`) {
		t.Errorf("lote existente: %d %s", rec.Code, rec.Body)
	}
}

func TestErroDeInfraVira500SemVazarDetalhe(t *testing.T) {
	h := server(&fakeReader{err: errors.New("senha do banco: hunter2")}, fakeThumbs{})
	rec := call(h, "GET", "/batches")
	if rec.Code != 500 || code(t, rec) != "internal_error" || strings.Contains(rec.Body.String(), "hunter2") {
		t.Errorf("500 vazou detalhe ou code errado: %d %s", rec.Code, rec.Body)
	}
}

func TestRetry(t *testing.T) {
	cases := []struct {
		res  app.RetryResult
		want int
		code string
	}{
		{app.Reopened, 202, ""}, {app.NotFound, 404, "photo_not_found"}, {app.NotFailed, 409, "photo_not_failed"},
	}
	for _, c := range cases {
		r := &fakeReader{retry: c.res}
		rec := call(server(r, fakeThumbs{}), "POST", "/dlq/"+id1+"/retry")
		if rec.Code != c.want || (c.code != "" && code(t, rec) != c.code) {
			t.Errorf("retry %v: %d %s", c.res, rec.Code, rec.Body)
		}
		if r.retried != id1 {
			t.Errorf("id repassado = %q", r.retried)
		}
	}
	// id malformado nem chega ao store
	r := &fakeReader{}
	if rec := call(server(r, fakeThumbs{}), "POST", "/dlq/xx/retry"); rec.Code != 404 || r.retried != "" {
		t.Errorf("id malformado: %d, store chamado com %q", rec.Code, r.retried)
	}
	// só POST
	if rec := call(server(&fakeReader{}, fakeThumbs{}), "GET", "/dlq/"+id1+"/retry"); rec.Code != 405 {
		t.Errorf("GET no retry: %d", rec.Code)
	}
}

func TestMiniaturaETagE304(t *testing.T) {
	h := server(&fakeReader{}, fakeThumbs{body: []byte("jpegjpeg")})
	rec := call(h, "GET", "/photos/"+id1+"/thumb")
	etag := rec.Header().Get("ETag")
	if rec.Code != 200 || etag == "" || rec.Header().Get("Content-Type") != "image/jpeg" ||
		rec.Header().Get("Cache-Control") != "private, max-age=0, must-revalidate" || rec.Body.String() != "jpegjpeg" {
		t.Fatalf("200: %d %v", rec.Code, rec.Header())
	}
	rec = call(h, "GET", "/photos/"+id1+"/thumb", "If-None-Match", etag)
	if rec.Code != 304 || rec.Body.Len() != 0 {
		t.Errorf("304: %d corpo %q", rec.Code, rec.Body)
	}
	if rec = call(h, "GET", "/photos/"+id1+"/thumb", "If-None-Match", `"outro"`); rec.Code != 200 {
		t.Errorf("etag diferente: %d", rec.Code)
	}
	// conteúdo diferente ⇒ ETag diferente (é hash do conteúdo, não do caminho)
	other := call(server(&fakeReader{}, fakeThumbs{body: []byte("outra")}), "GET", "/photos/"+id1+"/thumb").Header().Get("ETag")
	if other == etag {
		t.Error("ETag igual para conteúdos diferentes")
	}
}

func TestMiniaturaAusente(t *testing.T) {
	rec := call(server(&fakeReader{}, fakeThumbs{err: app.ErrNotFound}), "GET", "/photos/"+id1+"/thumb")
	if rec.Code != 404 || code(t, rec) != "thumb_not_found" {
		t.Errorf("%d %s", rec.Code, rec.Body)
	}
}
