//go:build faultinject

// Package fault: versão de teste (tag faultinject). WORKER_TEST_KILL_AT=<ponto> mata o
// processo com SIGKILL ao passar pelo ponto, simulando uma queda sem cleanup.
package fault

import (
	"os"
	"syscall"
)

// Pontos onde o teste pode matar o processo.
const (
	AfterTmp    = "after_tmp"
	AfterRename = "after_rename"
	AfterCommit = "after_commit"
)

// At mata o processo se WORKER_TEST_KILL_AT for este ponto. O marcador em
// WORKER_TEST_KILL_ONCE (arquivo) faz o kill acontecer uma só vez: o worker reiniciado segue.
func At(point string) {
	if os.Getenv("WORKER_TEST_KILL_AT") != point {
		return
	}
	if marker := os.Getenv("WORKER_TEST_KILL_ONCE"); marker != "" {
		f, err := os.OpenFile(marker, //nolint:gosec // caminho vem do ambiente de teste (build faultinject)
			os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
		if err != nil {
			return // já matou antes
		}
		_ = f.Close()
	}
	_ = syscall.Kill(os.Getpid(), syscall.SIGKILL)
}

// Relaxed libera loopback e HTTP no fetcher (o servidor do dataset roda em 127.0.0.1).
func Relaxed() bool { return os.Getenv("WORKER_TEST_RELAX_FETCH") == "1" }
