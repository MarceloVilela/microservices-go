// Package logging cria o *slog.Logger JSON usado por todos os serviços.
package logging

import (
	"io"
	"log/slog"
)

// New devolve um logger JSON no nível dado ("debug", "info", "warn" ou "error")
// com o atributo service em todas as linhas. Nível desconhecido vira info.
func New(w io.Writer, service, level string) *slog.Logger {
	var l slog.Level
	if err := l.UnmarshalText([]byte(level)); err != nil {
		l = slog.LevelInfo
	}
	h := slog.NewJSONHandler(w, &slog.HandlerOptions{Level: l})
	return slog.New(h).With("service", service)
}
