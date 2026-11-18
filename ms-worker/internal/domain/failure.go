// Package domain tem o vocabulário do worker: a classificação das falhas (ADR 0005).
package domain

import "errors"

// Class diz o que fazer com uma falha de processamento.
type Class string

const (
	// Transient: pode dar certo depois (timeout, 5xx, 429). Vai para o degrau de retry.
	Transient Class = "transient"
	// Permanent: não adianta tentar de novo (404, imagem inválida, host fora da allowlist,
	// pixels acima do teto). Vai direto à DLQ e a foto conta como terminada.
	Permanent Class = "permanent"
	// Exhausted: era transitória, mas os degraus acabaram.
	Exhausted Class = "exhausted"
)

// Failure é um erro de processamento já classificado. Reason é curto e seguro para
// gravar no banco e na DLQ (sem URL com credencial, sem dado do usuário).
type Failure struct {
	Class  Class
	Reason string
	Err    error
}

func (f *Failure) Error() string {
	if f.Err == nil {
		return f.Reason
	}
	return f.Reason + ": " + f.Err.Error()
}

// Unwrap expõe a causa.
func (f *Failure) Unwrap() error { return f.Err }

// NewTransient cria uma falha transitória.
func NewTransient(reason string, err error) error {
	return &Failure{Class: Transient, Reason: reason, Err: err}
}

// NewPermanent cria uma falha definitiva.
func NewPermanent(reason string, err error) error {
	return &Failure{Class: Permanent, Reason: reason, Err: err}
}

// Classify devolve a classe e o motivo de err. Um erro sem classificação é tratado como
// transitório: o degrau é limitado, então o custo do engano é pequeno, e o contrário (DLQ
// por erro de rede) perderia a foto.
func Classify(err error) (Class, string) {
	var f *Failure
	if errors.As(err, &f) {
		return f.Class, f.Reason
	}
	return Transient, "erro não classificado"
}
