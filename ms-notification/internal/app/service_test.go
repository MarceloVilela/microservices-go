package app_test

import (
	"context"
	"errors"
	"log/slog"
	"sync"
	"testing"

	"github.com/MarceloVilela/microservices-go/ms-notification/internal/app"
	"github.com/MarceloVilela/microservices-go/platform/messages"
)

const okBody = `{"v":1,"message_id":"m1","batch_id":"0b9a8f4e-5f0d-4a43-9d1b-6a2c3f6e1a10","total":3,"failed":1}`

type notifier struct {
	name  string
	err   error
	mu    sync.Mutex
	calls []messages.BatchCompleted
}

func (n *notifier) Name() string { return n.name }
func (n *notifier) Notify(_ context.Context, m messages.BatchCompleted) error {
	n.mu.Lock()
	defer n.mu.Unlock()
	n.calls = append(n.calls, m)
	return n.err
}

type store struct {
	seen      map[string]string
	seenErr   error // falha antes de rodar fn
	recordErr error // falha ao gravar, depois de fn
}

func newStore() *store { return &store{seen: map[string]string{}} }

func (s *store) Once(ctx context.Context, id string, fn func(context.Context) string) (bool, error) {
	if s.seenErr != nil {
		return false, s.seenErr
	}
	if _, ok := s.seen[id]; ok {
		return false, nil
	}
	le := fn(ctx)
	if s.recordErr != nil {
		return false, s.recordErr
	}
	s.seen[id] = le
	return true, nil
}

func svc(st *store, ns ...app.Notifier) *app.Service {
	return &app.Service{Store: st, Notifiers: ns, Log: slog.New(slog.DiscardHandler)}
}

func TestMensagemNovaNotificaTodosEGrava(t *testing.T) {
	a, b, st := &notifier{name: "a"}, &notifier{name: "b"}, newStore()
	if act := svc(st, a, b).Handle(context.Background(), []byte(okBody)); act != app.Ack {
		t.Fatalf("ação = %v", act)
	}
	if len(a.calls) != 1 || len(b.calls) != 1 {
		t.Errorf("chamadas a=%d b=%d", len(a.calls), len(b.calls))
	}
	if a.calls[0].Total != 3 || a.calls[0].Failed != 1 {
		t.Errorf("payload perdido: %+v", a.calls[0])
	}
	if e, ok := st.seen["0b9a8f4e-5f0d-4a43-9d1b-6a2c3f6e1a10"]; !ok || e != "" {
		t.Errorf("registro = %q, %v", e, ok)
	}
}

func TestMensagemRepetidaNaoNotificaDeNovo(t *testing.T) {
	a, st := &notifier{name: "a"}, newStore()
	s := svc(st, a)
	s.Handle(context.Background(), []byte(okBody))
	if act := s.Handle(context.Background(), []byte(okBody)); act != app.Ack {
		t.Fatalf("ação = %v", act)
	}
	if len(a.calls) != 1 {
		t.Errorf("notificou %d vezes, esperava 1", len(a.calls))
	}
}

func TestDestinoComFalhaNaoImpedeOsDemaisEFicaRegistrado(t *testing.T) {
	bad, good, st := &notifier{name: "smtp", err: errors.New("conexão recusada")}, &notifier{name: "slog"}, newStore()
	if act := svc(st, bad, good).Handle(context.Background(), []byte(okBody)); act != app.Ack {
		t.Fatalf("ação = %v", act)
	}
	if len(good.calls) != 1 {
		t.Error("o destino saudável não foi chamado")
	}
	if e := st.seen["0b9a8f4e-5f0d-4a43-9d1b-6a2c3f6e1a10"]; e == "" {
		t.Error("a falha do destino não ficou em last_error")
	}
}

func TestMensagemInvalidaVaiParaAckSemNotificar(t *testing.T) {
	for name, body := range map[string]string{
		"json quebrado":     `{`,
		"sem batch_id":      `{"v":1,"total":1,"failed":0}`,
		"batch_id não uuid": `{"v":1,"batch_id":"x","total":1,"failed":0}`,
		"total zero":        `{"v":1,"batch_id":"0b9a8f4e-5f0d-4a43-9d1b-6a2c3f6e1a10","total":0,"failed":0}`,
		"failed > total":    `{"v":1,"batch_id":"0b9a8f4e-5f0d-4a43-9d1b-6a2c3f6e1a10","total":1,"failed":2}`,
		"failed negativo":   `{"v":1,"batch_id":"0b9a8f4e-5f0d-4a43-9d1b-6a2c3f6e1a10","total":1,"failed":-1}`,
	} {
		t.Run(name, func(t *testing.T) {
			a, st := &notifier{name: "a"}, newStore()
			if act := svc(st, a).Handle(context.Background(), []byte(body)); act != app.Ack {
				t.Errorf("ação = %v, esperava Ack (reentregar não conserta)", act)
			}
			if len(a.calls) != 0 || len(st.seen) != 0 {
				t.Error("não deveria notificar nem gravar")
			}
		})
	}
}

func TestBancoForaNoSeenDevolveAFila(t *testing.T) {
	a, st := &notifier{name: "a"}, newStore()
	st.seenErr = errors.New("banco fora")
	if act := svc(st, a).Handle(context.Background(), []byte(okBody)); act != app.Requeue {
		t.Fatalf("ação = %v", act)
	}
	if len(a.calls) != 0 {
		t.Error("não deveria notificar sem saber se já notificou")
	}
}

func TestBancoForaNoRecordDevolveAFila(t *testing.T) {
	a, st := &notifier{name: "a"}, newStore()
	st.recordErr = errors.New("banco fora")
	if act := svc(st, a).Handle(context.Background(), []byte(okBody)); act != app.Requeue {
		t.Fatalf("ação = %v", act)
	}
}

func TestContextoCanceladoDevolveAFilaSemNotificar(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	a, st := &notifier{name: "a"}, newStore()
	if act := svc(st, a).Handle(ctx, []byte(okBody)); act != app.Requeue {
		t.Fatalf("ação = %v", act)
	}
	if len(a.calls) != 0 {
		t.Error("notificou com o serviço desligando")
	}
}
