// Package outbox implementa o relay do padrão transactional outbox (ADR 0012):
// lê linhas pendentes com FOR UPDATE SKIP LOCKED, publica com confirm do broker
// e só então marca published_at. A publicação é at-least-once.
package outbox

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"
)

// ErrRejected marca a falha que é culpa da própria linha (mensagem sem rota, por exemplo),
// e não da infraestrutura. Só ela conta para a quarentena: uma queda do broker não pode
// pôr linhas boas de lado.
var ErrRejected = errors.New("mensagem rejeitada")

// DefaultMaxAttempts é o número de rejeições da mesma linha antes da quarentena.
const DefaultMaxAttempts = 10

// Message é uma linha pendente do outbox.
type Message struct {
	ID         int64
	MessageID  string // identidade da mensagem (payload.message_id); vai no MessageId do AMQP
	RoutingKey string
	Payload    []byte
}

// Publisher publica uma mensagem e só retorna nil depois do confirm do broker.
type Publisher interface {
	Publish(ctx context.Context, m Message) error
}

// PublisherFunc adapta uma função a Publisher.
type PublisherFunc func(ctx context.Context, m Message) error

// Publish chama f.
func (f PublisherFunc) Publish(ctx context.Context, m Message) error { return f(ctx, m) }

// Store abre transações sobre o outbox.
type Store interface {
	Begin(ctx context.Context) (Tx, error)
}

// Tx é uma transação do relay; as linhas lidas por Pending ficam travadas até o fim dela.
type Tx interface {
	Pending(ctx context.Context, limit int) ([]Message, error)
	MarkPublished(ctx context.Context, ids []int64) error
	// Reject registra uma rejeição da linha e, ao atingir maxAttempts, a põe em quarentena
	// (parked_at), de modo que Pending deixa de devolvê-la. Informa se ficou em quarentena.
	Reject(ctx context.Context, id int64, reason string, maxAttempts int) (parked bool, err error)
	Commit(ctx context.Context) error
	Rollback(ctx context.Context) error
}

type rejection struct {
	id  int64
	err error
}

// Relay publica as linhas pendentes do outbox.
type Relay struct {
	Store        Store
	Publisher    Publisher
	BatchSize    int
	PollInterval time.Duration
	// PublishTimeout limita cada publicação (com o confirm). Sem prazo, um broker que não
	// confirma deixaria o relay preso com a transação e os locks abertos. Zero usa 10 s.
	PublishTimeout time.Duration
	// MaxAttempts é o limite de rejeições antes da quarentena. Zero usa DefaultMaxAttempts.
	MaxAttempts int
	Log         *slog.Logger
}

const defaultPublishTimeout = 10 * time.Second

func (r *Relay) publish(ctx context.Context, m Message) error {
	d := r.PublishTimeout
	if d <= 0 {
		d = defaultPublishTimeout
	}
	ctx, cancel := context.WithTimeout(ctx, d)
	defer cancel()
	return r.Publisher.Publish(ctx, m)
}

const (
	minBackoff = time.Second
	maxBackoff = 30 * time.Second
)

// Once publica um lote. Devolve quantas mensagens foram publicadas **e marcadas**.
// Se uma publicação falha, as já confirmadas do lote são marcadas e commitadas
// (para não republicar à toa) e o erro é devolvido; a que falhou e as seguintes
// continuam pendentes. Um erro no commit depois do confirm duplica no próximo
// ciclo, o que o at-least-once admite (o worker é idempotente, ADR 0006).
func (r *Relay) Once(ctx context.Context) (int, error) {
	tx, err := r.Store.Begin(ctx)
	if err != nil {
		return 0, fmt.Errorf("abrir transação: %w", err)
	}
	// o contexto do Rollback não é o da chamada: ele precisa funcionar também no cancelamento
	rollback := func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }

	pending, err := tx.Pending(ctx, r.BatchSize)
	if err != nil {
		rollback()
		return 0, fmt.Errorf("ler pendentes: %w", err)
	}
	var ids []int64
	var rejects []rejection
	var pubErr error
	for _, m := range pending {
		err := r.publish(ctx, m)
		if err == nil {
			ids = append(ids, m.ID)
			continue
		}
		if errors.Is(err, ErrRejected) {
			// possível culpa da linha: segue com as demais (uma linha ruim não trava o outbox)
			rejects = append(rejects, rejection{m.ID, err})
			continue
		}
		pubErr = fmt.Errorf("publicar outbox %d: %w", m.ID, err)
		break
	}
	// Só conta a rejeição se o mesmo ciclo publicou outras linhas: se TUDO foi rejeitado, a causa
	// provável é a topologia ausente (broker novo, topology-init por rodar), não as linhas. Contar
	// aí poria linhas boas em quarentena em segundos. Nesse caso devolve erro (backoff) e não conta.
	if len(ids) == 0 && len(rejects) > 0 && pubErr == nil {
		rollback()
		return 0, fmt.Errorf("todas as %d linhas foram rejeitadas (topologia ausente?): %w", len(rejects), rejects[0].err)
	}
	maxAttempts := r.MaxAttempts
	if maxAttempts <= 0 {
		maxAttempts = DefaultMaxAttempts
	}
	for _, rj := range rejects {
		parked, rerr := tx.Reject(context.WithoutCancel(ctx), rj.id, rj.err.Error(), maxAttempts)
		if rerr != nil {
			pubErr = fmt.Errorf("registrar rejeição do outbox %d: %w", rj.id, rerr)
			break
		}
		r.Log.Error("linha do outbox rejeitada", "outbox_id", rj.id, "erro", rj.err, "quarentena", parked)
	}
	if len(ids) == 0 && len(rejects) == 0 {
		rollback()
		return 0, pubErr
	}
	// marcar e commitar mesmo com o contexto cancelado: o confirm já aconteceu
	finish := context.WithoutCancel(ctx)
	if len(ids) > 0 {
		if err := tx.MarkPublished(finish, ids); err != nil {
			rollback()
			return 0, fmt.Errorf("marcar publicadas: %w", err)
		}
	}
	if err := tx.Commit(finish); err != nil {
		return 0, fmt.Errorf("commit: %w", err)
	}
	return len(ids), pubErr
}

// Run repete Once até o contexto terminar, devolvendo ctx.Err(). Em erro espera
// com backoff exponencial (1 s a 30 s); sem pendentes espera PollInterval.
func (r *Relay) Run(ctx context.Context) error {
	backoff := minBackoff
	for {
		n, err := r.Once(ctx)
		wait := r.PollInterval
		switch {
		case err != nil:
			r.Log.Warn("relay do outbox falhou", "erro", err, "publicadas", n, "proxima_tentativa", backoff.String())
			wait = backoff
			backoff = min(backoff*2, maxBackoff)
		case n >= r.BatchSize:
			wait = 0 // há mais; não dorme
			backoff = minBackoff
		default:
			backoff = minBackoff
		}
		if n > 0 && err == nil {
			r.Log.Debug("relay publicou", "n", n)
		}
		if wait > 0 {
			t := time.NewTimer(wait)
			select {
			case <-ctx.Done():
				t.Stop()
				return ctx.Err()
			case <-t.C:
			}
		} else if err := ctx.Err(); err != nil {
			return err
		}
	}
}
