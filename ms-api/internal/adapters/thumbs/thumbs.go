// Package thumbs lê as miniaturas que o ms-worker grava no volume (somente leitura aqui, ADR 0017).
package thumbs

import (
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"time"

	"github.com/MarceloVilela/microservices-go/ms-api/internal/app"
)

// MaxBytes é o teto de uma miniatura lida em memória (o worker gera JPEGs pequenos).
const MaxBytes = 5 << 20

// Store lê `<Dir>/<photo_id>.jpg`.
type Store struct{ Dir string }

// Read devolve o conteúdo e a data de modificação. O id é conferido (UUID) antes de tocar o disco,
// então nada com `..` ou `/` chega ao sistema de arquivos. Ausente ⇒ app.ErrNotFound.
func (s Store) Read(photoID string) ([]byte, time.Time, error) {
	if !app.ValidUUID(photoID) {
		return nil, time.Time{}, app.ErrNotFound
	}
	// os.Root impede que qualquer nome escape do diretório (defesa a mais, além do UUID)
	root, err := os.OpenRoot(s.Dir)
	if err != nil {
		return nil, time.Time{}, fmt.Errorf("abrir o diretório de miniaturas: %w", err)
	}
	defer func() { _ = root.Close() }()
	f, err := root.Open(photoID + ".jpg")
	if errors.Is(err, fs.ErrNotExist) {
		return nil, time.Time{}, app.ErrNotFound
	}
	if err != nil {
		return nil, time.Time{}, fmt.Errorf("abrir a miniatura: %w", err)
	}
	defer func() { _ = f.Close() }()
	info, err := f.Stat()
	if err != nil {
		return nil, time.Time{}, fmt.Errorf("stat da miniatura: %w", err)
	}
	if !info.Mode().IsRegular() || info.Size() > MaxBytes {
		return nil, time.Time{}, fmt.Errorf("miniatura %s inválida (modo %v, %d bytes)", photoID, info.Mode(), info.Size())
	}
	b, err := io.ReadAll(io.LimitReader(f, MaxBytes+1))
	if err != nil {
		return nil, time.Time{}, fmt.Errorf("ler a miniatura: %w", err)
	}
	return b, info.ModTime(), nil
}

// Check confere que o diretório existe (usado no /readyz).
func (s Store) Check() error {
	info, err := os.Stat(s.Dir)
	if err != nil {
		return fmt.Errorf("diretório de miniaturas: %w", err)
	}
	if !info.IsDir() {
		return fmt.Errorf("diretório de miniaturas: %s não é diretório", s.Dir)
	}
	return nil
}
