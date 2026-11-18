package outbox_test

import (
	"context"
	"errors"
	"log/slog"
	"testing"
	"time"

	"github.com/MarceloVilela/microservices-go/platform/outbox"
)

type fakeTx struct {
	msgs      []outbox.Message
	marked    []int64
	committed bool
	rolled    bool
	markErr   error
}

func (t *fakeTx) Pending(context.Context, int) ([]outbox.Message, error) { return t.msgs, nil }
func (t *fakeTx) MarkPublished(_ context.Context, ids []int64) error {
	t.marked = append(t.marked, ids...)
	return t.markErr
}
func (t *fakeTx) Commit(context.Context) error   { t.committed = true; return nil }
func (t *fakeTx) Rollback(context.Context) error { t.rolled = true; return nil }

type fakeStore struct{ tx *fakeTx }

func (s fakeStore) Begin(context.Context) (outbox.Tx, error) { return s.tx, nil }

type fakePub struct {
	sent   []int64
	failAt int64 // id que falha (0 = nenhum)
}

func (p *fakePub) Publish(_ context.Context, m outbox.Message) error {
	if m.ID == p.failAt {
		return errors.New("confirm negado")
	}
	p.sent = append(p.sent, m.ID)
	return nil
}

func relay(tx *fakeTx, pub *fakePub) *outbox.Relay {
	return &outbox.Relay{Store: fakeStore{tx}, Publisher: pub, BatchSize: 10, PollInterval: time.Millisecond,
		Log: slog.New(slog.DiscardHandler)}
}

func msgs(ids ...int64) []outbox.Message {
	var out []outbox.Message
	for _, id := range ids {
		out = append(out, outbox.Message{ID: id, RoutingKey: "process", Payload: []byte("{}")})
	}
	return out
}

func TestOncePublicaEMarcaDepoisDoConfirm(t *testing.T) {
	tx, pub := &fakeTx{msgs: msgs(1, 2, 3)}, &fakePub{}
	n, err := relay(tx, pub).Once(context.Background())
	if err != nil || n != 3 {
		t.Fatalf("n=%d err=%v", n, err)
	}
	if len(tx.marked) != 3 || !tx.committed {
		t.Errorf("marked=%v committed=%v", tx.marked, tx.committed)
	}
}

func TestOnceConfirmNegadoNaoMarcaAMensagem(t *testing.T) {
	tx, pub := &fakeTx{msgs: msgs(1, 2, 3)}, &fakePub{failAt: 2}
	n, err := relay(tx, pub).Once(context.Background())
	if err == nil {
		t.Fatal("esperava erro")
	}
	// só a 1 foi confirmada: ela é marcada e commitada; 2 e 3 continuam pendentes
	if n != 1 || len(tx.marked) != 1 || tx.marked[0] != 1 || !tx.committed {
		t.Errorf("n=%d marked=%v committed=%v", n, tx.marked, tx.committed)
	}
}

func TestOnceFalhaNaPrimeiraFazRollback(t *testing.T) {
	tx, pub := &fakeTx{msgs: msgs(1, 2)}, &fakePub{failAt: 1}
	if _, err := relay(tx, pub).Once(context.Background()); err == nil {
		t.Fatal("esperava erro")
	}
	if len(tx.marked) != 0 || tx.committed || !tx.rolled {
		t.Errorf("marked=%v committed=%v rolled=%v", tx.marked, tx.committed, tx.rolled)
	}
}

func TestOnceSemPendentes(t *testing.T) {
	tx := &fakeTx{}
	n, err := relay(tx, &fakePub{}).Once(context.Background())
	if err != nil || n != 0 || !tx.rolled {
		t.Errorf("n=%d err=%v rolled=%v", n, err, tx.rolled)
	}
}

func TestRunTerminaComOContexto(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
	defer cancel()
	tx := &fakeTx{}
	if err := relay(tx, &fakePub{}).Run(ctx); !errors.Is(err, context.DeadlineExceeded) {
		t.Errorf("Run = %v", err)
	}
}

type hangPub struct{}

func (hangPub) Publish(ctx context.Context, _ outbox.Message) error {
	<-ctx.Done() // broker que nunca confirma
	return ctx.Err()
}

func TestOnceBrokerQueNaoConfirmaNaoPrendeORelay(t *testing.T) {
	tx := &fakeTx{msgs: msgs(1, 2)}
	r := relay(tx, &fakePub{})
	r.Publisher, r.PublishTimeout = hangPub{}, 50*time.Millisecond
	start := time.Now()
	n, err := r.Once(context.Background())
	if err == nil || n != 0 || !tx.rolled || tx.committed {
		t.Errorf("n=%d err=%v rolled=%v committed=%v", n, err, tx.rolled, tx.committed)
	}
	if d := time.Since(start); d > 2*time.Second {
		t.Errorf("Once demorou %s", d)
	}
}
