package health_test

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/MarceloVilela/microservices-go/platform/health"
)

func get(h http.Handler, path string) int {
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, nil))
	return rec.Code
}

func TestHealthzSempre200(t *testing.T) {
	h := health.Handler(func(context.Context) error { return errors.New("x") }, time.Second)
	if c := get(h, "/healthz"); c != 200 {
		t.Errorf("healthz = %d", c)
	}
}

func TestReadyz(t *testing.T) {
	ok := health.Handler(func(context.Context) error { return nil }, time.Second)
	if c := get(ok, "/readyz"); c != 200 {
		t.Errorf("readyz ok = %d", c)
	}
	bad := health.Handler(func(context.Context) error { return errors.New("fila ausente") }, time.Second)
	if c := get(bad, "/readyz"); c != 503 {
		t.Errorf("readyz com erro = %d", c)
	}
}

func TestReadyzRespeitaTimeout(t *testing.T) {
	slow := health.Handler(func(ctx context.Context) error { <-ctx.Done(); return ctx.Err() }, 50*time.Millisecond)
	start := time.Now()
	if c := get(slow, "/readyz"); c != 503 {
		t.Errorf("readyz lento = %d", c)
	}
	if time.Since(start) > time.Second {
		t.Error("não respeitou o timeout")
	}
}

func TestMetodoNaoGetNao200(t *testing.T) {
	h := health.Handler(func(context.Context) error { return nil }, time.Second)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/healthz", nil))
	if rec.Code != http.StatusMethodNotAllowed {
		t.Errorf("POST = %d", rec.Code)
	}
}
