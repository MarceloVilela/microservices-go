//go:build !faultinject

// Package fault tem os pontos de falha injetável dos testes de integração (matar o worker
// em cada ponto da ordem de operações). Sem a tag faultinject, At não faz nada: o binário
// de produção e a imagem final nunca a incluem.
package fault

// Pontos onde o teste pode matar o processo.
const (
	AfterTmp    = "after_tmp"    // miniatura gravada no temporário, antes do rename
	AfterRename = "after_rename" // depois do rename, antes da transação
	AfterCommit = "after_commit" // depois do commit, antes do ack
)

// At é um no-op fora do teste.
func At(string) {}

// Relaxed informa se o fetcher deve aceitar loopback e HTTP (só no teste).
func Relaxed() bool { return false }
