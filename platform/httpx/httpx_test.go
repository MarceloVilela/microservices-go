package httpx_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/MarceloVilela/microservices-go/platform/httpx"
)

var ok = http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusNoContent) })

func do(h http.Handler, method string, hdr map[string]string, body string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, "/x", strings.NewReader(body))
	for k, v := range hdr {
		req.Header.Set(k, v)
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

func TestProblem(t *testing.T) {
	rec := httptest.NewRecorder()
	httpx.Problem(rec, http.StatusBadRequest, "invalid_request", "detalhe")
	if rec.Code != 400 || rec.Header().Get("Content-Type") != "application/problem+json" {
		t.Fatalf("code=%d ct=%s", rec.Code, rec.Header().Get("Content-Type"))
	}
	var p map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &p); err != nil {
		t.Fatal(err)
	}
	if p["code"] != "invalid_request" || p["status"] != float64(400) || p["detail"] != "detalhe" {
		t.Errorf("corpo = %v", p)
	}
}

func TestTokenExigeBearer(t *testing.T) {
	h := httpx.Token("segredo")(ok)
	cases := []struct {
		name   string
		method string
		hdr    map[string]string
		want   int
	}{
		{"sem cabeçalho", "POST", nil, 401},
		{"errado", "POST", map[string]string{"Authorization": "Bearer outro"}, 401},
		{"esquema errado", "POST", map[string]string{"Authorization": "segredo"}, 401},
		{"certo", "POST", map[string]string{"Authorization": "Bearer segredo"}, 204},
		{"preflight passa sem token", "OPTIONS", nil, 204},
	}
	for _, c := range cases {
		if got := do(h, c.method, c.hdr, "").Code; got != c.want {
			t.Errorf("%s: %d, quer %d", c.name, got, c.want)
		}
	}
}

func TestTokenVazioNaoExige(t *testing.T) {
	if got := do(httpx.Token("")(ok), "POST", nil, "").Code; got != 204 {
		t.Errorf("sem token configurado = %d", got)
	}
}

func TestCORS(t *testing.T) {
	h := httpx.CORS([]string{"http://localhost:4200"})(ok)
	rec := do(h, "POST", map[string]string{"Origin": "http://localhost:4200"}, "")
	if rec.Header().Get("Access-Control-Allow-Origin") != "http://localhost:4200" || rec.Header().Get("Vary") != "Origin" {
		t.Errorf("origem permitida: %v", rec.Header())
	}
	rec = do(h, "POST", map[string]string{"Origin": "http://evil.example"}, "")
	if rec.Header().Get("Access-Control-Allow-Origin") != "" {
		t.Errorf("origem não permitida recebeu CORS")
	}
	rec = do(h, "OPTIONS", map[string]string{"Origin": "http://localhost:4200", "Access-Control-Request-Method": "POST"}, "")
	if rec.Code != 204 || !strings.Contains(rec.Header().Get("Access-Control-Allow-Headers"), "Idempotency-Key") {
		t.Errorf("preflight: %d %v", rec.Code, rec.Header())
	}
}

func TestCORSSemOrigensNaoAdicionaCabecalho(t *testing.T) {
	rec := do(httpx.CORS(nil)(ok), "POST", map[string]string{"Origin": "http://x"}, "")
	if rec.Header().Get("Access-Control-Allow-Origin") != "" {
		t.Error("CORS vazio deve ficar desligado")
	}
}

func TestLimitBody(t *testing.T) {
	read := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if _, err := httpx.ReadBody(w, r, 5); err != nil {
			httpx.Problem(w, httpx.StatusForBodyError(err), "x", "x")
			return
		}
		w.WriteHeader(204)
	})
	if got := do(read, "POST", nil, "12345").Code; got != 204 {
		t.Errorf("no limite = %d", got)
	}
	if got := do(read, "POST", nil, "123456").Code; got != 413 {
		t.Errorf("acima do limite = %d", got)
	}
}
