// Package domain tem as regras do lote que não dependem de banco nem de HTTP.
package domain

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/url"
	"strings"
)

const (
	// MaxPhotos é o teto de fotos por lote.
	MaxPhotos = 100
	// MaxURLLen é o tamanho máximo de uma source_url.
	MaxURLLen = 2048
	// MaxKeyLen é o tamanho máximo da Idempotency-Key.
	MaxKeyLen = 128
)

// ValidationError é entrada inválida do cliente (vira 400).
type ValidationError struct{ Msg string }

func (e *ValidationError) Error() string { return e.Msg }

func invalid(format string, a ...any) error { return &ValidationError{Msg: fmt.Sprintf(format, a...)} }

// PhotoRequest é uma foto do pedido.
type PhotoRequest struct {
	SourceURL string `json:"source_url"`
}

// Request é o corpo do POST /batches.
type Request struct {
	Photos []PhotoRequest `json:"photos"`
}

// Validate confere limites e URLs. Roda antes de qualquer dependência (banco, broker).
// allowed são os hosts permitidos (minúsculos); só https na porta 443, sem credencial.
func (r Request) Validate(allowed []string) error {
	if n := len(r.Photos); n == 0 || n > MaxPhotos {
		return invalid("photos deve ter de 1 a %d itens (recebeu %d)", MaxPhotos, n)
	}
	for i, p := range r.Photos {
		if err := validURL(p.SourceURL, allowed); err != nil {
			return invalid("photos[%d].source_url: %v", i, err)
		}
	}
	return nil
}

func validURL(raw string, allowed []string) error {
	if raw == "" || len(raw) > MaxURLLen {
		return fmt.Errorf("deve ter de 1 a %d bytes", MaxURLLen)
	}
	u, err := url.Parse(raw)
	if err != nil {
		return fmt.Errorf("URL inválida")
	}
	switch {
	case u.Scheme != "https":
		return fmt.Errorf("só https é aceito")
	case u.User != nil:
		return fmt.Errorf("URL com credencial não é aceita")
	case u.Hostname() == "":
		return fmt.Errorf("URL sem host")
	case u.Port() != "" && u.Port() != "443":
		return fmt.Errorf("só a porta 443 é aceita")
	}
	host := strings.ToLower(u.Hostname())
	for _, a := range allowed {
		if host == a {
			return nil
		}
	}
	return fmt.Errorf("host %q fora da allowlist", host)
}

// Hash identifica o conteúdo do pedido (para detectar a mesma Idempotency-Key com
// corpo diferente). Cada URL entra com o tamanho na frente, então "a\nb" não
// colide com ["a","b"]. A ordem conta: outra ordem é outro lote.
func (r Request) Hash() string {
	h := sha256.New()
	for _, p := range r.Photos {
		_, _ = fmt.Fprintf(h, "%d:%s", len(p.SourceURL), p.SourceURL)
	}
	return hex.EncodeToString(h.Sum(nil))
}

// ValidKey confere a Idempotency-Key: 1 a 128 caracteres de [A-Za-z0-9._-].
func ValidKey(k string) bool {
	if k == "" || len(k) > MaxKeyLen {
		return false
	}
	for _, c := range k {
		switch {
		case c >= 'a' && c <= 'z', c >= 'A' && c <= 'Z', c >= '0' && c <= '9', c == '.', c == '_', c == '-':
		default:
			return false
		}
	}
	return true
}

// NewID gera um UUIDv4.
func NewID() string {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		panic(err) // crypto/rand não falha em sistemas suportados
	}
	b[6] = b[6]&0x0f | 0x40
	b[8] = b[8]&0x3f | 0x80
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:])
}
