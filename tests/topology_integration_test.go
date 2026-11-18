//go:build integration

// Testes de integração da topologia contra um RabbitMQ 4 real
// (testcontainers-go). Rodar com: make integration
package tests

import (
	"context"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	amqp "github.com/rabbitmq/amqp091-go"
	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/modules/rabbitmq"

	"github.com/MarceloVilela/microservices-go/platform/amqpx"
	"github.com/MarceloVilela/microservices-go/platform/topology"
)

var (
	once   sync.Once
	rbURL  string
	rbErr  error
	rbCont *rabbitmq.RabbitMQContainer
)

func TestMain(m *testing.M) {
	code := m.Run()
	if rbCont != nil {
		_ = testcontainers.TerminateContainer(rbCont)
	}
	os.Exit(code)
}

// broker sobe um RabbitMQ compartilhado pelo pacote. Como na S1, a primeira
// partida pode falhar com eacces no .erlang.cookie, então tenta de novo.
func broker(t *testing.T) string {
	t.Helper()
	once.Do(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
		defer cancel()
		for range 3 {
			rbCont, rbErr = rabbitmq.Run(ctx, "rabbitmq:4-management-alpine",
				testcontainers.CustomizeRequest(testcontainers.GenericContainerRequest{
					ContainerRequest: testcontainers.ContainerRequest{User: "rabbitmq"}}))
			if rbErr == nil {
				rbURL, rbErr = rbCont.AmqpURL(ctx)
				return
			}
			if rbCont != nil {
				_ = testcontainers.TerminateContainer(rbCont)
				rbCont = nil
			}
		}
	})
	if rbErr != nil {
		t.Fatalf("rabbitmq: %v", rbErr)
	}
	return rbURL
}

// reset apaga tudo que a topologia cria, para os testes não dependerem da ordem.
func reset(t *testing.T, url string) {
	t.Helper()
	conn, err := amqp.Dial(url)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	ch, err := conn.Channel()
	if err != nil {
		t.Fatal(err)
	}
	defer ch.Close()
	for _, q := range topology.Queues() {
		if _, err := ch.QueueDelete(q.Name, false, false, false); err != nil {
			t.Fatal(err)
		}
	}
	if err := ch.ExchangeDelete(topology.Exchange, false, false); err != nil {
		t.Fatal(err)
	}
}

func declare(t *testing.T, url string) error {
	t.Helper()
	conn, err := amqpx.Dial(context.Background(), url)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	return amqpx.Declare(context.Background(), conn)
}

func all() amqpx.Requirement {
	return amqpx.Requirement{Exchange: true, Queues: topology.Queues()}
}

func TestDeclareCriaTudoEEIdempotente(t *testing.T) {
	url := broker(t)
	reset(t, url)
	if err := declare(t, url); err != nil {
		t.Fatalf("1ª declaração: %v", err)
	}
	if err := declare(t, url); err != nil {
		t.Fatalf("2ª declaração (idempotência): %v", err)
	}
	ctx := context.Background()
	if err := amqpx.Verify(ctx, url, all()); err != nil {
		t.Fatalf("Verify depois do Declare: %v", err)
	}
}

func TestVerifyFalhaComFilaAusenteENaoRecria(t *testing.T) {
	url := broker(t)
	reset(t, url)
	if err := declare(t, url); err != nil {
		t.Fatal(err)
	}
	conn, err := amqp.Dial(url)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	ch, _ := conn.Channel()
	if _, err := ch.QueueDelete(topology.PhotoDLQ.Name, false, false, false); err != nil {
		t.Fatal(err)
	}

	for range 2 { // a 2ª chamada prova que a 1ª não recriou a fila
		err := amqpx.Verify(context.Background(), url, all())
		if err == nil || !strings.Contains(err.Error(), topology.PhotoDLQ.Name) {
			t.Fatalf("esperava erro citando %s, veio %v", topology.PhotoDLQ.Name, err)
		}
	}
	// o serviço que não exige a DLQ continua pronto
	only := amqpx.Requirement{Queues: []topology.Queue{topology.BatchCompleted}}
	if err := amqpx.Verify(context.Background(), url, only); err != nil {
		t.Errorf("Verify só de batch.completed: %v", err)
	}
}

func TestVerifySemExchangeFalha(t *testing.T) {
	url := broker(t)
	reset(t, url)
	err := amqpx.Verify(context.Background(), url, amqpx.Requirement{Exchange: true})
	if err == nil {
		t.Fatal("esperava erro: exchange não existe")
	}
}

func TestDeclareFalhaComArgumentosDivergentes(t *testing.T) {
	url := broker(t)
	reset(t, url)
	conn, err := amqp.Dial(url)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	ch, _ := conn.Channel()
	// mesma fila de retry, TTL diferente
	if _, err := ch.QueueDeclare("photo.process.retry.5s", true, false, false, false, amqp.Table{"x-message-ttl": int32(1)}); err != nil {
		t.Fatal(err)
	}
	err = declare(t, url)
	if err == nil || !strings.Contains(err.Error(), "PRECONDITION_FAILED") {
		t.Fatalf("esperava PRECONDITION_FAILED, veio %v", err)
	}
}

func TestVerifyComBrokerForaDoAr(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if err := amqpx.Verify(ctx, "amqp://u:p@127.0.0.1:1/", all()); err == nil {
		t.Fatal("esperava erro de conexão")
	}
}
