// Package health expõe /healthz (liveness) e /readyz (readiness).
package health

import (
	"context"
	"net/http"
	"time"
)

// Check é a verificação de prontidão; deve respeitar o contexto.
type Check func(ctx context.Context) error

// Handler devolve o mux (o serviço pode registrar mais rotas nele) com /healthz (sempre 200 se o processo responde) e
// /readyz (200 se check passar dentro de timeout; 503 caso contrário).
//
// O corpo do 503 é genérico: o detalhe da falha fica no log do serviço, não na
// resposta (evita vazar nome de host ou credencial).
func Handler(check Check, timeout time.Duration) *http.ServeMux {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok\n"))
	})
	mux.HandleFunc("GET /readyz", func(w http.ResponseWriter, r *http.Request) {
		ctx, cancel := context.WithTimeout(r.Context(), timeout)
		defer cancel()
		if err := check(ctx); err != nil {
			http.Error(w, "not ready", http.StatusServiceUnavailable)
			return
		}
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok\n"))
	})
	return mux
}
