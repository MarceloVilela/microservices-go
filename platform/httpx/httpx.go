// Package httpx reúne o que os serviços HTTP compartilham: erros
// application/problem+json, CORS por allowlist, token de demonstração e leitura
// de corpo com limite. Só stdlib (ADR 0008).
package httpx

import (
	"crypto/sha256"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"slices"
	"strings"
)

// Problem escreve um erro application/problem+json com um code estável, que o
// cliente pode mapear sem depender do texto.
func Problem(w http.ResponseWriter, status int, code, detail string) {
	w.Header().Set("Content-Type", "application/problem+json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]any{
		"type":   "about:blank",
		"title":  http.StatusText(status),
		"status": status,
		"code":   code,
		"detail": detail,
	})
}

// Middleware envolve um handler.
type Middleware func(http.Handler) http.Handler

// Token exige "Authorization: Bearer <token>". Token vazio desliga a checagem.
// OPTIONS (preflight do CORS) passa sem credencial, porque o navegador não a envia.
// A comparação é em tempo constante (sobre o hash, para não vazar o tamanho).
func Token(token string) Middleware {
	want := sha256.Sum256([]byte(token))
	return func(next http.Handler) http.Handler {
		if token == "" {
			return next
		}
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.Method != http.MethodOptions {
				got, found := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer ")
				sum := sha256.Sum256([]byte(got))
				if !found || subtle.ConstantTimeCompare(sum[:], want[:]) != 1 {
					w.Header().Set("WWW-Authenticate", "Bearer")
					Problem(w, http.StatusUnauthorized, "unauthorized", "token ausente ou inválido")
					return
				}
			}
			next.ServeHTTP(w, r)
		})
	}
}

// CORS libera só as origens da lista. Lista vazia desliga o CORS (nenhum
// cabeçalho é adicionado). O preflight é respondido aqui, com 204.
func CORS(origins []string) Middleware {
	return func(next http.Handler) http.Handler {
		if len(origins) == 0 {
			return next
		}
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Add("Vary", "Origin")
			origin := r.Header.Get("Origin")
			if origin != "" && slices.Contains(origins, origin) {
				w.Header().Set("Access-Control-Allow-Origin", origin)
				w.Header().Set("Access-Control-Expose-Headers", "Idempotent-Replay")
				if r.Method == http.MethodOptions && r.Header.Get("Access-Control-Request-Method") != "" {
					w.Header().Set("Access-Control-Allow-Methods", "GET, POST, OPTIONS")
					w.Header().Set("Access-Control-Allow-Headers", "Authorization, Content-Type, Idempotency-Key, Last-Event-ID")
					w.Header().Set("Access-Control-Max-Age", "600")
					w.WriteHeader(http.StatusNoContent)
					return
				}
			}
			next.ServeHTTP(w, r)
		})
	}
}

// OriginGuard recusa (403 origin_not_allowed) toda requisição que traga um Origin fora da lista,
// inclusive o preflight e o GET do SSE: o CORS sozinho só deixa de adicionar cabeçalhos, e o
// navegador é quem bloqueia. Sem Origin (curl, outro serviço) passa; a autenticação é do Token.
// Lista vazia recusa qualquer Origin. Usado pelo ms-api, que expõe leitura e reprocessamento.
func OriginGuard(origins []string) Middleware {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if o := r.Header.Get("Origin"); o != "" && !slices.Contains(origins, o) {
				Problem(w, http.StatusForbidden, "origin_not_allowed", "origem não permitida")
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

// Chain aplica os middlewares na ordem dada (o primeiro é o mais externo).
func Chain(h http.Handler, mws ...Middleware) http.Handler {
	for _, m := range slices.Backward(mws) {
		h = m(h)
	}
	return h
}

// ReadBody lê o corpo inteiro até max bytes; acima disso devolve *http.MaxBytesError.
func ReadBody(w http.ResponseWriter, r *http.Request, max int64) ([]byte, error) {
	return io.ReadAll(http.MaxBytesReader(w, r.Body, max))
}

// StatusForBodyError mapeia um erro de ReadBody: 413 para corpo grande, 400 para o resto.
func StatusForBodyError(err error) int {
	var tooBig *http.MaxBytesError
	if errors.As(err, &tooBig) {
		return http.StatusRequestEntityTooLarge
	}
	return http.StatusBadRequest
}
