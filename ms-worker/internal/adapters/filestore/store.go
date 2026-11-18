// Package filestore grava as miniaturas de forma atômica (ADR 0006 e 0017): arquivo
// temporário no mesmo diretório, fsync, rename para o nome definitivo (determinístico por
// photo_id) e fsync do diretório. Quem lê nunca vê um arquivo pela metade.
package filestore

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"time"

	"github.com/MarceloVilela/microservices-go/ms-worker/internal/fault"
)

var validID = regexp.MustCompile(`^[A-Za-z0-9-]{1,64}$`)

// Store implementa app.Thumbs sobre um diretório.
type Store struct{ Dir string }

// Path é o nome definitivo da miniatura da foto.
func (s Store) Path(photoID string) string { return filepath.Join(s.Dir, photoID+".jpg") }

// Save grava jpeg como a miniatura de photoID. Repetir com o mesmo conteúdo é inofensivo
// (reentrega): o rename troca o arquivo atomicamente.
func (s Store) Save(photoID string, jpeg []byte) (err error) {
	if !validID.MatchString(photoID) {
		return fmt.Errorf("photo_id inválido para nome de arquivo: %q", photoID)
	}
	f, err := os.CreateTemp(s.Dir, photoID+".*.tmp")
	if err != nil {
		return fmt.Errorf("criar temporário: %w", err)
	}
	tmp := f.Name()
	defer func() {
		if err != nil {
			_ = os.Remove(tmp)
		}
	}()
	if _, err = f.Write(jpeg); err != nil {
		_ = f.Close()
		return fmt.Errorf("escrever temporário: %w", err)
	}
	if err = f.Sync(); err != nil {
		_ = f.Close()
		return fmt.Errorf("fsync do temporário: %w", err)
	}
	if err = f.Close(); err != nil {
		return fmt.Errorf("fechar temporário: %w", err)
	}
	fault.At(fault.AfterTmp)
	if err = os.Rename(tmp, s.Path(photoID)); err != nil {
		return fmt.Errorf("rename: %w", err)
	}
	if err = syncDir(s.Dir); err != nil {
		return err
	}
	fault.At(fault.AfterRename)
	return nil
}

func syncDir(dir string) error {
	d, err := os.Open(dir) //nolint:gosec // dir é THUMBS_DIR, configuração do operador
	if err != nil {
		return fmt.Errorf("abrir diretório: %w", err)
	}
	defer func() { _ = d.Close() }()
	if err := d.Sync(); err != nil {
		return fmt.Errorf("fsync do diretório: %w", err)
	}
	return nil
}

// CleanTmp apaga temporários órfãos (de um worker que caiu) mais velhos que olderThan.
// O limite de idade protege o temporário em uso por outra réplica no mesmo volume.
func (s Store) CleanTmp(olderThan time.Duration) (int, error) {
	files, err := filepath.Glob(filepath.Join(s.Dir, "*.tmp"))
	if err != nil {
		return 0, err
	}
	n := 0
	for _, name := range files {
		info, err := os.Stat(name)
		if err != nil || time.Since(info.ModTime()) < olderThan {
			continue
		}
		if err := os.Remove(name); err == nil {
			n++
		}
	}
	return n, nil
}

// Check confirma que o diretório existe e aceita gravação (usado no /readyz).
func (s Store) Check() error {
	f, err := os.CreateTemp(s.Dir, ".ready-*.tmp")
	if err != nil {
		return fmt.Errorf("diretório de miniaturas não gravável: %w", err)
	}
	name := f.Name()
	err = errors.Join(f.Close(), os.Remove(name))
	return err
}
