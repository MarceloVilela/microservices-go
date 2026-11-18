// Package amqpx isola o uso de amqp091-go: conexão, declaração da topologia
// (só para o topology-init) e verificação passiva (para os demais serviços).
//
// A reconexão com backoff (ADR 0003) entra na Fase 4; aqui cada chamada abre e
// fecha a própria conexão.
package amqpx

import (
	"context"
	"errors"
	"fmt"
	"net"
	"time"

	amqp "github.com/rabbitmq/amqp091-go"

	"github.com/MarceloVilela/microservices-go/platform/topology"
)

// ValidURL confere o esquema e o formato de uma AMQP_URL sem ecoar o valor
// (pode conter senha).
func ValidURL(s string) error {
	if _, err := amqp.ParseURI(s); err != nil {
		return errors.New("não é uma URL amqp:// ou amqps:// válida")
	}
	return nil
}

// handshakeTimeout vale quando o contexto não tem prazo (o padrão do amqp091).
const handshakeTimeout = 30 * time.Second

// Dial conecta respeitando o contexto: o prazo cobre o TCP **e** o handshake
// AMQP. Um Dial customizado remove o deadline padrão do amqp091, então ele é
// recolocado aqui (a biblioteca o zera ao fim do handshake). Sem isso, um
// broker que aceita TCP mas não completa o handshake (partida) bloquearia
// o chamador para sempre.
func Dial(ctx context.Context, url string) (*amqp.Connection, error) {
	var d net.Dialer
	return amqp.DialConfig(url, amqp.Config{
		Dial: func(network, addr string) (net.Conn, error) {
			conn, err := d.DialContext(ctx, network, addr)
			if err != nil {
				return nil, err
			}
			deadline, ok := ctx.Deadline()
			if !ok {
				deadline = time.Now().Add(handshakeTimeout)
			}
			if err := conn.SetDeadline(deadline); err != nil {
				_ = conn.Close()
				return nil, err
			}
			return conn, nil
		},
	})
}

// IsPermanent diz se o erro não melhora com nova tentativa: argumentos
// divergentes de uma fila existente (PRECONDITION_FAILED) ou acesso recusado.
// Falhas de conexão e canal durante a partida do broker são transitórias.
func IsPermanent(err error) bool {
	var ae *amqp.Error
	if !errors.As(err, &ae) {
		return false
	}
	return ae.Code == amqp.PreconditionFailed || ae.Code == amqp.AccessRefused
}

// abortOnDone fecha a conexão se ctx terminar, o que interrompe qualquer RPC
// pendente (os RPCs do amqp091 não recebem contexto). Chame o stop devolvido.
func abortOnDone(ctx context.Context, conn *amqp.Connection) (stop func() bool) {
	return context.AfterFunc(ctx, func() { _ = conn.Close() })
}

// Declare cria exchange, filas e bindings. Só o topology-init pode chamar.
// É idempotente; argumentos divergentes de uma fila já existente falham com
// PRECONDITION_FAILED, de propósito: o erro aparece no boot.
func Declare(ctx context.Context, conn *amqp.Connection) error {
	defer abortOnDone(ctx, conn)()
	ch, err := conn.Channel()
	if err != nil {
		return fmt.Errorf("abrir canal: %w", err)
	}
	defer func() { _ = ch.Close() }()

	if err := ch.ExchangeDeclare(topology.Exchange, "direct", true, false, false, false, nil); err != nil {
		return fmt.Errorf("declarar exchange %s: %w", topology.Exchange, err)
	}
	for _, q := range topology.Queues() {
		if _, err := ch.QueueDeclare(q.Name, true, false, false, false, amqp.Table(q.Args)); err != nil {
			return fmt.Errorf("declarar fila %s: %w", q.Name, err)
		}
		if err := ch.QueueBind(q.Name, q.Key, topology.Exchange, false, nil); err != nil {
			return fmt.Errorf("ligar fila %s: %w", q.Name, err)
		}
	}
	return nil
}

// Requirement é o que um serviço exige que exista no broker.
type Requirement struct {
	Exchange bool
	Queues   []topology.Queue
}

// Verify confere com declaração passiva, que nunca cria nada. Um item ausente
// faz o broker fechar o canal, então cada item usa um canal novo.
func Verify(ctx context.Context, url string, req Requirement) error {
	conn, err := Dial(ctx, url)
	if err != nil {
		return fmt.Errorf("conectar ao broker: %w", err)
	}
	defer func() { _ = conn.Close() }()
	defer abortOnDone(ctx, conn)()

	var errs []error
	if req.Exchange {
		if err := passive(conn, func(ch *amqp.Channel) error {
			return ch.ExchangeDeclarePassive(topology.Exchange, "direct", true, false, false, false, nil)
		}); err != nil {
			errs = append(errs, fmt.Errorf("exchange %s: %w", topology.Exchange, err))
		}
	}
	for _, q := range req.Queues {
		if err := ctx.Err(); err != nil {
			return errors.Join(append(errs, err)...)
		}
		if err := passive(conn, func(ch *amqp.Channel) error {
			_, err := ch.QueueDeclarePassive(q.Name, true, false, false, false, nil)
			return err
		}); err != nil {
			errs = append(errs, fmt.Errorf("fila %s: %w", q.Name, err))
		}
	}
	return errors.Join(errs...)
}

func passive(conn *amqp.Connection, fn func(*amqp.Channel) error) error {
	ch, err := conn.Channel()
	if err != nil {
		return err
	}
	defer func() { _ = ch.Close() }()
	return fn(ch)
}
