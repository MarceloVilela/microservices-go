package outbox_test

import (
	"context"
	"errors"
	"fmt"
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
	rejected  map[int64]string // id -> motivo
	parked    map[int64]bool
	attempts  map[int64]int
}

func (t *fakeTx) Pending(context.Context, int) ([]outbox.Message, error) { return t.msgs, nil }
func (t *fakeTx) MarkPublished(_ context.Context, ids []int64) error {
	t.marked = append(t.marked, ids...)
	return t.markErr
}
func (t *fakeTx) Reject(_ context.Context, id int64, reason string, maxAttempts int) (bool, error) {
	if t.rejected == nil {
		t.rejected, t.parked, t.attempts = map[int64]string{}, map[int64]bool{}, map[int64]int{}
	}
	t.attempts[id]++
	t.rejected[id] = reason
	t.parked[id] = t.attempts[id] >= maxAttempts
	return t.parked[id], nil
}
func (t *fakeTx) Commit(context.Context) error   { t.committed = true; return nil }
func (t *fakeTx) Rollback(context.Context) error { t.rolled = true; return nil }

type fakeStore struct{ tx *fakeTx }

func (s fakeStore) Begin(context.Context) (outbox.Tx, error) { return s.tx, nil }

type fakePub struct {
	sent   []int64
	failAt int64 // id que falha (0 = nenhum)
	reject int64 // id que o broker rejeita por causa da própria mensagem (0 = nenhum)
}

func (p *fakePub) Publish(_ context.Context, m outbox.Message) error {
	if m.ID == p.failAt {
		return errors.New("confirm negado")
	}
	if m.ID == p.reject {
		return fmt.Errorf("%w: sem rota", outbox.ErrRejected)
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

func TestOnceLinhaVenenosaNaoTravaAsDemais(t *testing.T) {
	tx, pub := &fakeTx{msgs: msgs(1, 2, 3)}, &fakePub{reject: 2}
	n, err := relay(tx, pub).Once(context.Background())
	if err != nil || n != 2 {
		t.Fatalf("n=%d err=%v", n, err)
	}
	if len(tx.marked) != 2 || tx.marked[0] != 1 || tx.marked[1] != 3 || !tx.committed {
		t.Errorf("marked=%v committed=%v", tx.marked, tx.committed)
	}
	if tx.attempts[2] != 1 || tx.parked[2] {
		t.Errorf("tentativas=%d parked=%v (ainda abaixo do limite)", tx.attempts[2], tx.parked[2])
	}
}

func TestOnceTudoRejeitadoNaoContaTentativa(t *testing.T) {
	// topologia ausente: todas as linhas são "sem rota"; nenhuma pode ir para a quarentena
	tx, pub := &fakeTx{msgs: msgs(7)}, &fakePub{reject: 7}
	r := relay(tx, pub)
	r.MaxAttempts = 1
	n, err := r.Once(context.Background())
	if err == nil || n != 0 {
		t.Fatalf("n=%d err=%v, esperava erro (backoff)", n, err)
	}
	if tx.committed || len(tx.attempts) != 0 || len(tx.parked) != 0 {
		t.Errorf("committed=%v attempts=%v parked=%v: nada deveria ser contado", tx.committed, tx.attempts, tx.parked)
	}
}

func TestOnceLinhaVenenosaEntraEmQuarentenaNoLimite(t *testing.T) {
	tx, pub := &fakeTx{msgs: msgs(1, 2)}, &fakePub{reject: 2}
	r := relay(tx, pub)
	r.MaxAttempts = 1
	if n, err := r.Once(context.Background()); err != nil || n != 1 {
		t.Fatalf("n=%d err=%v", n, err)
	}
	if !tx.parked[2] || !tx.committed {
		t.Errorf("parked=%v committed=%v", tx.parked, tx.committed)
	}
}

func TestOnceFalhaDeConexaoNaoContaTentativa(t *testing.T) {
	tx, pub := &fakeTx{msgs: msgs(1)}, &fakePub{failAt: 1}
	if _, err := relay(tx, pub).Once(context.Background()); err == nil {
		t.Fatal("esperava erro")
	}
	if len(tx.attempts) != 0 {
		t.Errorf("erro de infraestrutura não pode levar a linha à quarentena: %v", tx.attempts)
	}
}
