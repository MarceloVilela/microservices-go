package initcmd_test

import (
	"bytes"
	"context"
	"testing"

	"github.com/MarceloVilela/microservices-go/ms-queues/internal/initcmd"
)

func TestConfigInvalidaSai1(t *testing.T) {
	var out, errb bytes.Buffer
	if code := initcmd.Run(context.Background(), nil, &out, &errb); code != 1 {
		t.Errorf("sem AMQP_URL deveria sair 1, saiu %d", code)
	}
}

func TestContextoCanceladoSai1(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	var out, errb bytes.Buffer
	if code := initcmd.Run(ctx, []string{"AMQP_URL=amqp://u:p@127.0.0.1:1/"}, &out, &errb); code != 1 {
		t.Errorf("contexto cancelado deveria sair 1, saiu %d", code)
	}
}
