package amqpx_test

import (
	"context"
	"errors"
	"fmt"
	"net"
	"testing"
	"time"

	amqp "github.com/rabbitmq/amqp091-go"

	"github.com/MarceloVilela/microservices-go/platform/amqpx"
	"github.com/MarceloVilela/microservices-go/platform/topology"
)

func TestIsPermanent(t *testing.T) {
	cases := []struct {
		name string
		err  error
		want bool
	}{
		{"nil", nil, false},
		{"precondition failed", &amqp.Error{Code: amqp.PreconditionFailed}, true},
		{"acesso recusado", &amqp.Error{Code: amqp.AccessRefused}, true},
		{"embrulhado", fmt.Errorf("declarar fila x: %w", &amqp.Error{Code: amqp.PreconditionFailed}), true},
		{"conexão forçada (broker reiniciando)", &amqp.Error{Code: amqp.ConnectionForced}, false},
		{"EOF", errors.New("EOF"), false},
	}
	for _, c := range cases {
		if got := amqpx.IsPermanent(c.err); got != c.want {
			t.Errorf("%s: IsPermanent = %v, esperado %v", c.name, got, c.want)
		}
	}
}

// Broker que aceita TCP mas nunca completa o handshake AMQP (janela de partida).
func TestDialRespeitaOPrazoDoContextoNoHandshake(t *testing.T) {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = l.Close() }()
	go func() {
		for {
			c, err := l.Accept()
			if err != nil {
				return
			}
			defer func() { _ = c.Close() }() // mantém aberta e muda
		}
	}()

	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancel()
	start := time.Now()
	_, err = amqpx.Dial(ctx, "amqp://u:p@"+l.Addr().String()+"/")
	if err == nil {
		t.Fatal("esperava erro de handshake")
	}
	if d := time.Since(start); d > 3*time.Second {
		t.Errorf("Dial demorou %v; deveria respeitar o prazo de 300ms", d)
	}
}

func TestVerifyRespeitaOPrazoDoContexto(t *testing.T) {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = l.Close() }()
	go func() {
		for {
			c, err := l.Accept()
			if err != nil {
				return
			}
			defer func() { _ = c.Close() }()
		}
	}()
	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancel()
	start := time.Now()
	err = amqpx.Verify(ctx, "amqp://u:p@"+l.Addr().String()+"/", amqpx.Requirement{Queues: []topology.Queue{topology.PhotoProcess}})
	if err == nil || time.Since(start) > 3*time.Second {
		t.Errorf("Verify deveria falhar rápido; err=%v, demorou %v", err, time.Since(start))
	}
}
