package filestore_test

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/MarceloVilela/microservices-go/ms-worker/internal/adapters/filestore"
)

func TestSaveGravaEAtualizaSemDeixarTemporario(t *testing.T) {
	dir := t.TempDir()
	s := filestore.Store{Dir: dir}
	for _, content := range []string{"v1", "v1"} { // a reentrega regrava o mesmo conteúdo
		if err := s.Save("abc-123", []byte(content)); err != nil {
			t.Fatal(err)
		}
	}
	got, err := os.ReadFile(s.Path("abc-123"))
	if err != nil || string(got) != "v1" {
		t.Fatalf("conteúdo = %q, %v", got, err)
	}
	if tmps, _ := filepath.Glob(filepath.Join(dir, "*.tmp")); len(tmps) != 0 {
		t.Errorf("sobrou temporário: %v", tmps)
	}
}

func TestSaveRecusaNomeQueSaiDoDiretorio(t *testing.T) {
	s := filestore.Store{Dir: t.TempDir()}
	for _, id := range []string{"../x", "a/b", "", ".", "a b"} {
		if err := s.Save(id, []byte("x")); err == nil {
			t.Errorf("id %q deveria ser recusado", id)
		}
	}
}

func TestSaveSemDiretorioFalhaELimpa(t *testing.T) {
	s := filestore.Store{Dir: filepath.Join(t.TempDir(), "nao-existe")}
	if err := s.Save("abc", []byte("x")); err == nil {
		t.Fatal("esperava erro")
	}
}

func TestCleanTmpSoApagaOsVelhos(t *testing.T) {
	dir := t.TempDir()
	old, fresh := filepath.Join(dir, "a.1.tmp"), filepath.Join(dir, "b.2.tmp")
	final := filepath.Join(dir, "c.jpg")
	for _, p := range []string{old, fresh, final} {
		if err := os.WriteFile(p, []byte("x"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	past := time.Now().Add(-time.Hour)
	if err := os.Chtimes(old, past, past); err != nil {
		t.Fatal(err)
	}
	n, err := filestore.Store{Dir: dir}.CleanTmp(time.Minute)
	if err != nil || n != 1 {
		t.Fatalf("n=%d err=%v", n, err)
	}
	for p, want := range map[string]bool{old: false, fresh: true, final: true} {
		if _, err := os.Stat(p); (err == nil) != want {
			t.Errorf("%s existe=%v, esperado %v", filepath.Base(p), err == nil, want)
		}
	}
}

func TestCheck(t *testing.T) {
	if err := (filestore.Store{Dir: t.TempDir()}).Check(); err != nil {
		t.Error(err)
	}
	if err := (filestore.Store{Dir: filepath.Join(t.TempDir(), "x")}).Check(); err == nil {
		t.Error("diretório inexistente deveria falhar")
	}
}
