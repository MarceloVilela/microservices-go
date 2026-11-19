// Package app é o caso de uso do worker: processa uma mensagem photo.process na ordem
// da ADR 0006 e decide o que fazer com o ack. Só conhece portas (interfaces definidas aqui).
package app

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"regexp"
	"time"

	"github.com/MarceloVilela/microservices-go/ms-worker/internal/domain"
	"github.com/MarceloVilela/microservices-go/ms-worker/internal/fault"
	"github.com/MarceloVilela/microservices-go/platform/messages"
)

// Status da foto no banco.
const (
	StatusPending = "pending"
	StatusDone    = "done"
	StatusFailed  = "failed"
)

// ErrNotFound: a foto da mensagem não existe no banco.
var ErrNotFound = errors.New("foto não encontrada")

// Finish é o resultado de uma foto, gravado na transação do passo 4.
type Finish struct {
	PhotoID    string
	Status     string // StatusDone ou StatusFailed
	FailClass  string
	FailReason string
}

// Outcome descreve o efeito da transação.
type Outcome struct {
	// Duplicate: a foto já estava terminada; nada foi gravado nem contado.
	Duplicate bool
	// BatchCompleted: esta transação fechou o lote (e gravou batch.completed no outbox).
	BatchCompleted bool
}

// Photos é o banco, do ponto de vista do worker.
type Photos interface {
	// Status devolve o status atual da foto, ou ErrNotFound.
	Status(ctx context.Context, photoID string) (string, error)
	// Finish grava, numa só transação: estado da foto, contador do lote, fim do lote
	// (outbox, uma vez) e eventos.
	Finish(ctx context.Context, f Finish) (Outcome, error)
	// MarkStalled registra a causa numa foto que foi à DLQ por infraestrutura esgotada e segue
	// pending (F7-5), para o ms-api listá-la em GET /dlq. Condicional a status = pending; não conta o lote.
	MarkStalled(ctx context.Context, photoID string, class domain.Class, reason string) error
}

// Recorder recebe o desfecho de cada mensagem (métricas). Opcional.
type Recorder interface {
	// Processed registra o desfecho e a duração do tratamento de uma mensagem.
	Processed(outcome string, d time.Duration)
}

// Desfechos reportados ao Recorder.
const (
	OutcomeDone    = "done"    // foto concluída
	OutcomeSkipped = "skipped" // já estava terminada
	OutcomeRetry   = "retry"   // foi para um degrau da escada
	OutcomeDLQ     = "dlq"     // foi para a DLQ
	OutcomeRequeue = "requeue" // devolvida à fila (desligamento ou broker/banco indisponível)
)

// Fetcher baixa a imagem. Erros vêm como *domain.Failure.
type Fetcher interface {
	Fetch(ctx context.Context, sourceURL string) ([]byte, error)
}

// Thumbnailer gera a miniatura (JPEG). Erros vêm como *domain.Failure.
type Thumbnailer interface {
	Thumbnail(src []byte) ([]byte, error)
}

// Thumbs grava a miniatura de forma atômica (temporário, fsync, rename).
type Thumbs interface {
	Save(photoID string, jpeg []byte) error
}

// Delivery é uma mensagem recebida.
type Delivery struct {
	Body      []byte
	MessageID string
	Attempt   int // degraus de retry já percorridos
}

// Router republica mensagens (cada método só retorna depois do confirm do broker).
type Router interface {
	// Retry manda d para o degrau step (0 a Steps-1); a próxima entrega terá Attempt = step+1.
	Retry(ctx context.Context, step int, d Delivery) error
	// DeadLetter manda d para a DLQ com a classe e o motivo da falha nos cabeçalhos.
	DeadLetter(ctx context.Context, d Delivery, class domain.Class, reason string) error
}

// Action é o que o consumidor deve fazer com a mensagem.
type Action int

const (
	// Ack: terminou (inclusive "já estava feito" e "foi republicada").
	Ack Action = iota
	// Requeue: problema de infraestrutura; devolve à fila sem consumir degrau.
	Requeue
)

// Processor processa uma mensagem de cada vez; é seguro para uso concorrente.
type Processor struct {
	Photos      Photos
	Fetcher     Fetcher
	Thumbnailer Thumbnailer
	Thumbs      Thumbs
	Router      Router
	// StepTTLs são os TTLs dos degraus de retry, crescentes (um por fila da escada). Vazio = sem retry.
	StepTTLs []time.Duration
	Log      *slog.Logger
	// Metrics é opcional.
	Metrics Recorder
}

// Handle executa a ordem de operações: (1) estado no banco, (2) baixar e gerar a miniatura,
// (3) gravar o arquivo, (4) transação no banco. O ack é do chamador, depois do retorno Ack.
func (p *Processor) Handle(ctx context.Context, d Delivery) Action {
	start := time.Now()
	act, outcome := p.handle(ctx, d)
	if p.Metrics != nil {
		p.Metrics.Processed(outcome, time.Since(start))
	}
	return act
}

func (p *Processor) handle(ctx context.Context, d Delivery) (Action, string) {
	msg, err := parse(d.Body)
	log := p.Log.With("message_id", d.MessageID, "attempt", d.Attempt)
	if err != nil {
		log.Error("mensagem inválida; indo para a DLQ", "erro", err)
		return p.deadLetterOnly(ctx, d, domain.Permanent, "mensagem inválida")
	}
	log = log.With("batch_id", msg.BatchID, "photo_id", msg.PhotoID)

	// 1. estado no banco
	status, err := p.Photos.Status(ctx, msg.PhotoID)
	switch {
	case errors.Is(err, ErrNotFound):
		log.Error("foto inexistente; indo para a DLQ")
		return p.deadLetterOnly(ctx, d, domain.Permanent, "foto inexistente")
	case err != nil:
		return p.failed(ctx, log, d, msg, domain.NewInfrastructure("banco indisponível", err))
	case status != StatusPending:
		log.Info("foto já terminada; só ack", "status", status)
		return Ack, OutcomeSkipped
	}

	// 2 e 3. baixar, gerar e gravar a miniatura
	if err := p.render(ctx, msg); err != nil {
		if ctx.Err() != nil {
			return Requeue, OutcomeRequeue // desligando: não gasta degrau
		}
		var f *domain.Failure
		if !errors.As(err, &f) {
			f = domain.NewInfrastructure("disco indisponível", err)
		}
		return p.failed(ctx, log, d, msg, f)
	}

	// 4. transação
	out, err := p.Photos.Finish(ctx, Finish{PhotoID: msg.PhotoID, Status: StatusDone})
	if err != nil {
		return p.failed(ctx, log, d, msg, domain.NewInfrastructure("banco indisponível", err))
	}
	fault.At(fault.AfterCommit)
	log.Info("foto concluída", "duplicada", out.Duplicate, "lote_completo", out.BatchCompleted)
	return Ack, OutcomeDone
}

func (p *Processor) render(ctx context.Context, msg messages.PhotoProcess) error {
	img, err := p.Fetcher.Fetch(ctx, msg.SourceURL)
	if err != nil {
		return classified(err)
	}
	thumb, err := p.Thumbnailer.Thumbnail(img)
	if err != nil {
		return classified(err)
	}
	if err := p.Thumbs.Save(msg.PhotoID, thumb); err != nil {
		return fmt.Errorf("gravar miniatura: %w", err)
	}
	return nil
}

// classified garante um *domain.Failure: o erro sem classe do download ou da miniatura é transitório.
func classified(err error) error {
	var f *domain.Failure
	if errors.As(err, &f) {
		return err
	}
	return domain.NewTransient("erro não classificado", err)
}

// failed decide o destino de uma falha. Transitória e de infraestrutura consomem degrau (com
// Retry-After pulando degraus); esgotadas, vão à DLQ. Só a transitória esgotada e a definitiva
// fecham a foto como failed: com a infraestrutura fora, a foto fica pending para reprocessar.
func (p *Processor) failed(ctx context.Context, log *slog.Logger, d Delivery, msg messages.PhotoProcess, f *domain.Failure) (Action, string) {
	class, reason := f.Class, f.Reason
	if class == domain.Transient || class == domain.Infrastructure {
		if ctx.Err() != nil {
			return Requeue, OutcomeRequeue // desligando: não gasta degrau
		}
		if d.Attempt < len(p.StepTTLs) {
			step := StepFor(d.Attempt, f.RetryAfter, p.StepTTLs)
			if err := p.Router.Retry(ctx, step, d); err != nil {
				log.Warn("republicar no degrau de retry", "erro", err)
				return Requeue, OutcomeRequeue
			}
			log.Info("falha; indo para o degrau", "classe", class, "degrau", step, "retry_after", f.RetryAfter, "motivo", reason, "erro", f.Err)
			return Ack, OutcomeRetry
		}
		if class == domain.Transient {
			class, reason = domain.Exhausted, "tentativas esgotadas: "+reason
		} else {
			reason = "tentativas esgotadas: " + reason
		}
	}
	log.Warn("falha sem retry; DLQ", "classe", class, "motivo", reason, "erro", f.Err)
	closes := class != domain.Infrastructure
	if closes {
		// D2 (Fase 4): reler o estado antes da DLQ evita mandar para lá uma foto que uma entrega
		// concorrente já concluiu. A janela que sobra é só entre esta leitura e o Finish.
		if st, err := p.Photos.Status(ctx, msg.PhotoID); err == nil && st != StatusPending {
			log.Info("foto já terminada por outra entrega; só ack", "status", st)
			return Ack, OutcomeSkipped
		}
	}
	// DLQ antes da transação e do ack: se morrer no meio, a reentrega repete e a DLQ pode ter duplicata
	if err := p.Router.DeadLetter(ctx, d, class, reason); err != nil {
		log.Warn("publicar na DLQ", "erro", err)
		return Requeue, OutcomeRequeue
	}
	if !closes {
		// F7-5: a foto segue pending; a causa fica gravada para o ms-api listá-la na DLQ. Best effort:
		// com o banco fora (a causa mais provável) devolver Requeue faria laço quente e uma
		// duplicata na DLQ a cada volta; sem a marca a foto só continua invisível, como antes.
		if err := p.Photos.MarkStalled(ctx, msg.PhotoID, class, reason); err != nil {
			log.Error("marcar a foto parada na DLQ (ela segue invisível em GET /dlq)", "erro", err)
		}
		return Ack, OutcomeDLQ
	}
	if _, err := p.Photos.Finish(ctx, Finish{PhotoID: msg.PhotoID, Status: StatusFailed, FailClass: string(class), FailReason: reason}); err != nil {
		log.Warn("gravar falha da foto", "erro", err)
		return Requeue, OutcomeRequeue
	}
	fault.At(fault.AfterCommit)
	return Ack, OutcomeDLQ
}

// deadLetterOnly trata a mensagem que não dá para ligar a uma foto: DLQ e ack.
func (p *Processor) deadLetterOnly(ctx context.Context, d Delivery, class domain.Class, reason string) (Action, string) {
	if err := p.Router.DeadLetter(ctx, d, class, reason); err != nil {
		p.Log.Warn("publicar na DLQ", "erro", err)
		return Requeue, OutcomeRequeue
	}
	return Ack, OutcomeDLQ
}

var uuidRE = regexp.MustCompile(`^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$`)

func parse(body []byte) (messages.PhotoProcess, error) {
	var m messages.PhotoProcess
	if err := json.Unmarshal(body, &m); err != nil {
		return m, fmt.Errorf("json: %w", err)
	}
	if m.V != messages.Version || m.SourceURL == "" {
		return m, errors.New("campos obrigatórios ausentes ou versão desconhecida")
	}
	// UUID inválido faria o Postgres falhar sempre; como erro de banco vira Requeue, a mensagem
	// voltaria à fila para sempre. Recusar aqui a manda à DLQ como definitiva.
	if !uuidRE.MatchString(m.PhotoID) || !uuidRE.MatchString(m.BatchID) {
		return m, errors.New("photo_id ou batch_id não é UUID")
	}
	return m, nil
}
