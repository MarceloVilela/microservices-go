package metrics_test

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/MarceloVilela/microservices-go/platform/metrics"
)

func scrape(t *testing.T, h http.Handler) string {
	t.Helper()
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/metrics", nil))
	b, _ := io.ReadAll(rec.Body)
	return string(b)
}

// O rótulo "route" é o padrão do ServeMux, nunca o caminho cru (cardinalidade, F7-3).
func TestMiddlewareUsaPadraoDaRota(t *testing.T) {
	reg := metrics.NewRegistry()
	mux := http.NewServeMux()
	mux.HandleFunc("GET /photos/{id}/thumb", func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusTeapot) })
	h := reg.Middleware(mux)

	for _, p := range []string{"/photos/a/thumb", "/photos/b/thumb", "/nada/aqui/1", "/nada/aqui/2"} {
		h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, p, nil))
	}
	out := scrape(t, reg.Handler())

	if !strings.Contains(out, `http_requests_total{code="418",route="GET /photos/{id}/thumb"} 2`) {
		t.Fatalf("contador por padrão de rota ausente:\n%s", out)
	}
	if !strings.Contains(out, `http_requests_total{code="404",route="unmatched"} 2`) {
		t.Fatalf("rota sem padrão deve virar \"unmatched\":\n%s", out)
	}
	for _, leak := range []string{"/photos/a", "/nada/aqui"} {
		if strings.Contains(out, leak) {
			t.Fatalf("caminho cru %q vazou para um rótulo", leak)
		}
	}
	if !strings.Contains(out, "http_request_seconds_bucket") {
		t.Fatal("histograma de latência ausente")
	}
}

// O SSE precisa continuar fazendo Flush através do middleware.
func TestMiddlewarePreservaFlush(t *testing.T) {
	reg := metrics.NewRegistry()
	flushed := false
	h := reg.Middleware(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		if f, ok := w.(http.Flusher); ok {
			f.Flush()
			flushed = true
		}
		if err := http.NewResponseController(w).Flush(); err != nil {
			t.Errorf("ResponseController não alcança o writer original: %v", err)
		}
	}))
	h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/x", nil))
	if !flushed {
		t.Fatal("o wrapper escondeu http.Flusher")
	}
}

func TestRegistryTemColetoresDeProcesso(t *testing.T) {
	out := scrape(t, metrics.NewRegistry().Handler())
	if !strings.Contains(out, "go_goroutines") {
		t.Fatal("sem métricas do runtime Go")
	}
}

// Dois registros não compartilham estado (nada de registro global).
func TestRegistrosIndependentes(t *testing.T) {
	a, b := metrics.NewRegistry(), metrics.NewRegistry()
	a.Counter("so_no_a_total", "x").Inc()
	if strings.Contains(scrape(t, b.Handler()), "so_no_a_total") {
		t.Fatal("registro vazou entre instâncias")
	}
}

func TestSnapshotCacheaPorTTL(t *testing.T) {
	calls := 0
	c := metrics.NewCache(func() (map[string]float64, error) {
		calls++
		return map[string]float64{"q": float64(calls)}, nil
	}, 5*60*1e9)
	c.Get()
	c.Get()
	if calls != 1 {
		t.Fatalf("a coleta deveria ser cacheada: %d chamadas", calls)
	}
}

func TestCacheGuardaOErroPeloTTL(t *testing.T) {
	calls := 0
	c := metrics.NewCache(func() (map[string]float64, error) {
		calls++
		return nil, io.ErrUnexpectedEOF
	}, 5*60*1e9)
	for range 3 {
		if _, err := c.Get(); err == nil {
			t.Fatal("o erro da coleta deve voltar")
		}
	}
	if calls != 1 {
		t.Fatalf("banco fora: a coleta deveria rodar uma vez dentro do ttl, rodou %d", calls)
	}
}

func TestGaugeOmiteQuandoFalha(t *testing.T) {
	reg := metrics.NewRegistry()
	reg.Gauge("bom", "x", func() (float64, error) { return 7, nil })
	reg.Gauge("ruim", "x", func() (float64, error) { return 0, io.ErrUnexpectedEOF })
	out := scrape(t, reg.Handler())
	if !strings.Contains(out, "\nbom 7\n") || strings.Contains(out, "\nruim ") {
		t.Fatalf("gauge com falha deve sumir sem derrubar o scrape:\n%s", out)
	}
}
