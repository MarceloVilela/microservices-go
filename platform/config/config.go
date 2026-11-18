// Package config lê e valida variáveis de ambiente de forma explícita.
//
// Todos os erros são acumulados (o operador vê tudo de uma vez) e nenhum valor
// aparece em mensagem de erro, só nomes.
package config

import (
	"errors"
	"fmt"
	"regexp"
	"slices"
	"strings"
	"time"
)

var validName = regexp.MustCompile(`^[A-Z][A-Z0-9_]*$`)

// Var descreve uma variável de ambiente.
type Var struct {
	Name     string
	Default  string
	Required bool
	Secret   bool // documenta que o valor nunca pode ser logado
	Validate func(string) error
}

// Values guarda os valores já validados.
type Values map[string]string

// String devolve o valor da variável.
func (v Values) String(name string) string { return v[name] }

// Duration devolve o valor como time.Duration; Load já garantiu que é válido
// para variáveis com ValidDuration, então erro de parse devolve zero.
func (v Values) Duration(name string) time.Duration {
	d, _ := time.ParseDuration(v[name])
	return d
}

// ValidDuration valida uma duração positiva.
func ValidDuration(s string) error {
	d, err := time.ParseDuration(s)
	if err != nil {
		return errors.New("duração inválida")
	}
	if d <= 0 {
		return errors.New("a duração deve ser positiva")
	}
	return nil
}

// OneOf devolve um validador que aceita só os valores dados.
func OneOf(options ...string) func(string) error {
	return func(s string) error {
		if slices.Contains(options, s) {
			return nil
		}
		return fmt.Errorf("valor fora de %s", strings.Join(options, "|"))
	}
}

// Names devolve os nomes das variáveis em ordem alfabética (nunca valores).
func Names(vars []Var) []string {
	names := make([]string, 0, len(vars))
	for _, v := range vars {
		names = append(names, v.Name)
	}
	slices.Sort(names)
	return names
}

// Load valida environ (formato "NOME=valor") contra vars.
//
// Além de obrigatórias e validadores, detecta nomes inválidos que seriam
// ignorados em silêncio (ex.: "http-addr" no lugar de "HTTP_ADDR").
func Load(environ []string, vars []Var) (Values, error) {
	var errs []error
	known := map[string]bool{}
	for _, v := range vars {
		if !validName.MatchString(v.Name) {
			errs = append(errs, fmt.Errorf("definição: nome %q inválido (use A-Z, 0-9 e _)", v.Name))
		}
		known[v.Name] = true
	}

	env := map[string]string{}
	for _, kv := range environ {
		name, value, ok := strings.Cut(kv, "=")
		if !ok {
			continue
		}
		if canon := strings.ToUpper(strings.NewReplacer("-", "_", ".", "_").Replace(name)); canon != name && known[canon] {
			errs = append(errs, fmt.Errorf("variável %q tem nome inválido; o esperado é %s", name, canon))
			continue
		}
		env[name] = value
	}

	out := Values{}
	for _, v := range vars {
		val, set := env[v.Name]
		if !set || val == "" {
			if v.Required {
				errs = append(errs, fmt.Errorf("%s é obrigatória", v.Name))
				continue
			}
			val = v.Default
		}
		if v.Validate != nil && val != "" {
			if err := v.Validate(val); err != nil {
				// o valor não entra na mensagem (pode ser segredo)
				errs = append(errs, fmt.Errorf("%s inválida: %w", v.Name, err))
				continue
			}
		}
		out[v.Name] = val
	}
	if len(errs) > 0 {
		return nil, errors.Join(errs...)
	}
	return out, nil
}
