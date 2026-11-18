package amqpx

import (
	"context"
	"errors"
	"fmt"
	"sync"

	amqp "github.com/rabbitmq/amqp091-go"

	"github.com/MarceloVilela/microservices-go/platform/topology"
)

// Publisher publica na exchange da topologia com confirm do broker. Conecta de
// forma preguiçosa e, depois de qualquer falha, descarta a conexão: a próxima
// chamada reconecta (o backoff é de quem chama, o relay).
type Publisher struct {
	URL string

	mu      sync.Mutex
	conn    *amqp.Connection
	ch      *amqp.Channel
	returns chan amqp.Return
}

// ErrUnroutable indica que o broker devolveu a mensagem (nenhuma fila ligada à chave).
var ErrUnroutable = errors.New("mensagem sem rota (nenhuma fila ligada à chave)")

// Publish envia body (persistente, JSON) com a chave dada e só retorna nil
// depois do ack do broker. messageID vai no cabeçalho MessageId do AMQP.
func (p *Publisher) Publish(ctx context.Context, routingKey, messageID string, body []byte) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	if err := p.connect(ctx); err != nil {
		return err
	}
	// mandatory: sem rota o broker devolve a mensagem em vez de descartá-la em silêncio
	conf, err := p.ch.PublishWithDeferredConfirmWithContext(ctx, topology.Exchange, routingKey, true, false, amqp.Publishing{
		ContentType:  "application/json",
		DeliveryMode: amqp.Persistent,
		MessageId:    messageID,
		Body:         body,
	})
	if err != nil {
		p.reset()
		return fmt.Errorf("publicar: %w", err)
	}
	acked, err := conf.WaitContext(ctx)
	if err != nil {
		p.reset()
		return fmt.Errorf("esperar confirm: %w", err)
	}
	if !acked {
		return errors.New("broker recusou a mensagem (nack)")
	}
	// o basic.return chega antes do ack, na mesma conexão
	select {
	case <-p.returns:
		return ErrUnroutable
	default:
	}
	return nil
}

// Close fecha a conexão, se houver.
func (p *Publisher) Close() {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.reset()
}

func (p *Publisher) reset() {
	if p.conn != nil {
		_ = p.conn.Close()
	}
	p.conn, p.ch, p.returns = nil, nil, nil
}

func (p *Publisher) connect(ctx context.Context) error {
	if p.conn != nil && !p.conn.IsClosed() && !p.ch.IsClosed() {
		return nil
	}
	p.reset()
	conn, err := Dial(ctx, p.URL)
	if err != nil {
		return fmt.Errorf("conectar ao broker: %w", err)
	}
	ch, err := conn.Channel()
	if err != nil {
		_ = conn.Close()
		return fmt.Errorf("abrir canal: %w", err)
	}
	if err := ch.Confirm(false); err != nil {
		_ = conn.Close()
		return fmt.Errorf("ligar confirms: %w", err)
	}
	p.conn, p.ch = conn, ch
	p.returns = ch.NotifyReturn(make(chan amqp.Return, 16))
	return nil
}
