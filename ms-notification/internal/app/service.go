// Package app é o caso de uso do ms-notification: notificar o fim de um lote uma vez por
// batch_id (ADR 0020), sem que a falha de um destino derrube o consumo.
package app

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"regexp"
	"strings"

	"github.com/MarceloVilela/microservices-go/platform/messages"
)

// Notifier é um destino da notificação (slog, SMTP...). Definido aqui, no consumidor.
type Notifier interface {
	Name() string
	Notify(ctx context.Context, m messages.BatchCompleted) error
}

// Store guarda quais lotes já foram notificados.
type Store interface {
	// Once roda fn só se o lote ainda não foi notificado e o marca como notificado, serializando
	// entregas concorrentes do mesmo batch_id (duas réplicas, reentrega com a anterior em voo).
	// fn devolve o resumo das falhas de destino ("" se nenhuma). ran=false: já estava notificado.
	// Se o processo morrer dentro de fn, nada é gravado e a reentrega notifica de novo (at-least-once).
	Once(ctx context.Context, batchID string, fn func(ctx context.Context) (lastErr string)) (ran bool, err error)
}

// Action é o que o consumidor faz com a mensagem.
type Action int

const (
	// Ack: terminou (inclusive "já notificado" e "mensagem inválida").
	Ack Action = iota
	// Requeue: o banco falhou ou o serviço está desligando; devolve à fila.
	Requeue
)

// Counter conta desfechos (métricas). Opcional.
type Counter interface{ Inc(outcome string) }

// Desfechos reportados ao Counter.
const (
	OutcomeNotified          = "notified"           // todos os destinos tentados e nenhum falhou
	OutcomeDestinationFailed = "destination_failed" // lote marcado como notificado, mas algum destino falhou
	OutcomeDuplicate         = "duplicate"          // lote já notificado
	OutcomeInvalid           = "invalid"            // mensagem descartada
	OutcomeRequeue           = "requeue"            // devolvida à fila
)

// Service trata mensagens batch.completed.
type Service struct {
	Store     Store
	Notifiers []Notifier
	Log       *slog.Logger
	Metrics   Counter
}

func (s *Service) count(outcome string) {
	if s.Metrics != nil {
		s.Metrics.Inc(outcome)
	}
}

var uuidRe = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`)

func parse(body []byte) (messages.BatchCompleted, error) {
	var m messages.BatchCompleted
	if err := json.Unmarshal(body, &m); err != nil {
		return m, fmt.Errorf("json: %w", err)
	}
	switch {
	case m.V != messages.Version:
		return m, fmt.Errorf("versão %d do contrato não suportada (esperada %d)", m.V, messages.Version)
	case !uuidRe.MatchString(m.BatchID):
		return m, errors.New("batch_id ausente ou não é UUID")
	case m.Total <= 0:
		return m, errors.New("total deve ser positivo")
	case m.Failed < 0 || m.Failed > m.Total:
		return m, errors.New("failed fora de 0..total")
	}
	return m, nil
}

// Handle segue a ordem da spec: valida e, sob a deduplicação (Store.Once), notifica todos os destinos.
func (s *Service) Handle(ctx context.Context, body []byte) Action {
	if ctx.Err() != nil {
		s.count(OutcomeRequeue)
		return Requeue // desligando: devolve sem notificar
	}
	m, err := parse(body)
	if err != nil {
		// sem DLQ para este evento e reentregar não conserta
		s.Log.Error("batch.completed inválida descartada", "erro", err)
		s.count(OutcomeInvalid)
		return Ack
	}
	log := s.Log.With("batch_id", m.BatchID, "message_id", m.MessageID)

	destFailed := false
	ran, err := s.Store.Once(ctx, m.BatchID, func(ctx context.Context) string {
		var failures []string
		for _, n := range s.Notifiers {
			if err := n.Notify(ctx, m); err != nil {
				// falha de destino nunca derruba o consumo nem impede os demais (ADR 0020)
				log.Error("destino falhou", "destino", n.Name(), "erro", err)
				failures = append(failures, n.Name()+": "+err.Error())
			}
		}
		destFailed = len(failures) > 0
		return strings.Join(failures, "; ")
	})
	if err != nil {
		log.Warn("deduplicação (banco fora?); a reentrega pode notificar de novo", "erro", err)
		s.count(OutcomeRequeue)
		return Requeue
	}
	switch {
	case !ran:
		log.Debug("lote já notificado; ignorando reentrega")
		s.count(OutcomeDuplicate)
	case destFailed:
		s.count(OutcomeDestinationFailed)
	default:
		s.count(OutcomeNotified)
	}
	return Ack
}
