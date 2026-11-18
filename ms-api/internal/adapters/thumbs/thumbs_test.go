package thumbs_test

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/MarceloVilela/microservices-go/ms-api/internal/adapters/thumbs"
	"github.com/MarceloVilela/microservices-go/ms-api/internal/app"
)

const id = "0192f0c0-1111-7222-8333-444455556666"

func TestReadLeAMiniatura(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, id+".jpg"), []byte("jpeg"), 0o600); err != nil {
		t.Fatal(err)
	}
	b, mod, err := thumbs.Store{Dir: dir}.Read(id)
	if err != nil || string(b) != "jpeg" || mod.IsZero() {
		t.Fatalf("Read = %q, %v, %v", b, mod, err)
	}
}

func TestReadAusenteEIdMalformado(t *testing.T) {
	dir := t.TempDir()
	// um arquivo fora do diretório que um id com ".." tentaria alcançar
	if err := os.WriteFile(filepath.Join(filepath.Dir(dir), "segredo.jpg"), []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	s := thumbs.Store{Dir: dir}
	for _, in := range []string{id, "../segredo", "..%2fsegredo", "", "abc"} {
		if _, _, err := s.Read(in); !errors.Is(err, app.ErrNotFound) {
			t.Errorf("Read(%q) = %v, quer ErrNotFound", in, err)
		}
	}
}

func TestReadRecusaDiretorioEArquivoGrande(t *testing.T) {
	dir := t.TempDir()
	if err := os.Mkdir(filepath.Join(dir, id+".jpg"), 0o700); err != nil {
		t.Fatal(err)
	}
	if _, _, err := (thumbs.Store{Dir: dir}).Read(id); err == nil || errors.Is(err, app.ErrNotFound) {
		t.Errorf("diretório no lugar do arquivo deveria dar erro de infra, deu %v", err)
	}
}

func TestCheck(t *testing.T) {
	if err := (thumbs.Store{Dir: t.TempDir()}).Check(); err != nil {
		t.Error(err)
	}
	if err := (thumbs.Store{Dir: "/nao/existe/mesmo"}).Check(); err == nil {
		t.Error("diretório ausente deveria falhar")
	}
}
