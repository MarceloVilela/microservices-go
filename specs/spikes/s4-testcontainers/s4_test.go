// Spike S4: testcontainers-go com PostgreSQL e RabbitMQ 4 sem segredo, e relay do outbox com
// FOR UPDATE SKIP LOCKED rodando em duas instâncias.
package s4

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
	"testing"
	"time"

	amqp "github.com/rabbitmq/amqp091-go"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/modules/postgres"
	"github.com/testcontainers/testcontainers-go/modules/rabbitmq"
)

const queue = "s4.photo.process"

type env struct {
	pgURL, amqpURL string
}

func start(t *testing.T) *env {
	t.Helper()
	ctx := context.Background()
	t0 := time.Now()
	pg, err := postgres.Run(ctx, "postgres:18-alpine",
		postgres.WithDatabase("spike"), postgres.WithUsername("spike"), postgres.WithPassword("spike"),
		postgres.BasicWaitStrategies())
	if err != nil {
		t.Fatalf("postgres: %v", err)
	}
	testcontainers.CleanupContainer(t, pg)
	t1 := time.Now()
	// a imagem do RabbitMQ local só sobe como usuário "rabbitmq" nesta máquina (ver S1)
	rb, err := rabbitmq.Run(ctx, "rabbitmq:4-management-alpine",
		testcontainers.CustomizeRequest(testcontainers.GenericContainerRequest{
			ContainerRequest: testcontainers.ContainerRequest{User: "rabbitmq"}}))
	if err != nil {
		t.Fatalf("rabbitmq: %v", err)
	}
	testcontainers.CleanupContainer(t, rb)
	t.Logf("subida: postgres=%v rabbitmq=%v", t1.Sub(t0).Round(time.Millisecond), time.Since(t1).Round(time.Millisecond))
	pgURL, err := pg.ConnectionString(ctx, "sslmode=disable")
	if err != nil {
		t.Fatal(err)
	}
	au, err := rb.AmqpURL(ctx)
	if err != nil {
		t.Fatal(err)
	}
	return &env{pgURL, au}
}

func (e *env) reset(t *testing.T, rows int) *pgxpool.Pool {
	t.Helper()
	ctx := context.Background()
	pool, err := pgxpool.New(ctx, e.pgURL)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	for _, q := range []string{
		`DROP TABLE IF EXISTS outbox`,
		`CREATE TABLE outbox(id bigserial PRIMARY KEY, topic text NOT NULL, payload jsonb NOT NULL,
		   created_at timestamptz NOT NULL DEFAULT now(), published_at timestamptz)`,
		fmt.Sprintf(`INSERT INTO outbox(topic,payload) SELECT 'photo.process', jsonb_build_object('n', g) FROM generate_series(1,%d) g`, rows),
	} {
		if _, err := pool.Exec(ctx, q); err != nil {
			t.Fatal(err)
		}
	}
	conn, _ := amqp.Dial(e.amqpURL)
	defer conn.Close()
	ch, _ := conn.Channel()
	ch.QueueDelete(queue, false, false, false)
	if _, err := ch.QueueDeclare(queue, true, false, false, false, nil); err != nil {
		t.Fatal(err)
	}
	return pool
}

// uma instância do relay: lote com SKIP LOCKED, publica com confirm, marca published_at, commit.
type relay struct {
	name  string
	pool  *pgxpool.Pool
	ch    *amqp.Channel
	count int
	ids   []int64
}

func newRelay(t *testing.T, e *env, name string, pool *pgxpool.Pool) *relay {
	conn, err := amqp.Dial(e.amqpURL)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { conn.Close() })
	ch, _ := conn.Channel()
	if err := ch.Confirm(false); err != nil {
		t.Fatal(err)
	}
	return &relay{name: name, pool: pool, ch: ch}
}

// batch devolve (linhas, tx aberta). Quem chama decide commit ou rollback.
func (r *relay) batch(ctx context.Context, size int) (int, pgx.Tx, error) {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return 0, nil, err
	}
	rows, err := tx.Query(ctx, `SELECT id, payload FROM outbox WHERE published_at IS NULL
		ORDER BY id LIMIT $1 FOR UPDATE SKIP LOCKED`, size)
	if err != nil {
		tx.Rollback(ctx)
		return 0, nil, err
	}
	type row struct {
		id      int64
		payload []byte
	}
	var rs []row
	for rows.Next() {
		var x row
		if err := rows.Scan(&x.id, &x.payload); err != nil {
			tx.Rollback(ctx)
			return 0, nil, err
		}
		rs = append(rs, x)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		tx.Rollback(ctx)
		return 0, nil, err
	}
	var dcs []*amqp.DeferredConfirmation
	for _, x := range rs {
		body, _ := json.Marshal(map[string]any{"outbox_id": x.id})
		dc, err := r.ch.PublishWithDeferredConfirmWithContext(ctx, "", queue, false, false,
			amqp.Publishing{DeliveryMode: amqp.Persistent, Body: body})
		if err != nil {
			tx.Rollback(ctx)
			return 0, nil, err
		}
		dcs = append(dcs, dc)
	}
	for _, dc := range dcs {
		if ok, err := dc.WaitContext(ctx); err != nil || !ok {
			tx.Rollback(ctx)
			return 0, nil, fmt.Errorf("sem confirm: %v %v", ok, err)
		}
	}
	for _, x := range rs {
		r.ids = append(r.ids, x.id)
	}
	r.count += len(rs)
	return len(rs), tx, nil
}

// run: laço normal (batch, mark, commit) até não haver mais linhas.
func (r *relay) run(ctx context.Context, size int) error {
	for {
		start := len(r.ids)
		n, tx, err := r.batch(ctx, size)
		if err != nil {
			return err
		}
		if n == 0 {
			return tx.Rollback(ctx)
		}
		if _, err := tx.Exec(ctx, `UPDATE outbox SET published_at = now() WHERE id = ANY($1)`, r.ids[start:]); err != nil {
			return err
		}
		if err := tx.Commit(ctx); err != nil {
			return err
		}
	}
}

// drain lê a fila e devolve quantas vezes cada outbox_id apareceu.
func drain(t *testing.T, e *env) map[int64]int {
	conn, err := amqp.Dial(e.amqpURL)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	ch, _ := conn.Channel()
	seen := map[int64]int{}
	for {
		m, ok, err := ch.Get(queue, true)
		if err != nil {
			t.Fatal(err)
		}
		if !ok {
			return seen
		}
		var p struct {
			ID int64 `json:"outbox_id"`
		}
		json.Unmarshal(m.Body, &p)
		seen[p.ID]++
	}
}

// duas instâncias do relay drenam 1.000 linhas: sem perda e sem duplicata, trabalho dividido.
func TestRelayDuasInstancias(t *testing.T) {
	e := start(t)
	const rows = 1000
	pool := e.reset(t, rows)
	ctx := context.Background()
	a, b := newRelay(t, e, "A", pool), newRelay(t, e, "B", pool)
	t0 := time.Now()
	var wg sync.WaitGroup
	for _, r := range []*relay{a, b} {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if err := r.run(ctx, 20); err != nil {
				t.Error(r.name, err)
			}
		}()
	}
	wg.Wait()
	d := time.Since(t0)
	seen := drain(t, e)
	dups, total := 0, 0
	for _, c := range seen {
		total += c
		if c > 1 {
			dups += c - 1
		}
	}
	var unpub int
	pool.QueryRow(ctx, `SELECT count(*) FROM outbox WHERE published_at IS NULL`).Scan(&unpub)
	t.Logf("2 relays (lote 20): A=%d B=%d, ids distintos na fila=%d, mensagens=%d, duplicatas=%d, sem published_at=%d, duração=%v",
		a.count, b.count, len(seen), total, dups, unpub, d.Round(time.Millisecond))
	if len(seen) != rows || total != rows || dups != 0 || unpub != 0 {
		t.Fatalf("esperado %d mensagens únicas", rows)
	}
	if a.count == 0 || b.count == 0 {
		t.Errorf("trabalho não foi dividido: A=%d B=%d", a.count, b.count)
	}
}

// A segura 50 linhas num lote aberto; B não as vê (SKIP LOCKED). A "cai" depois de publicar (rollback):
// as linhas voltam e B as publica de novo: duplicata (at-least-once), nenhuma perdida.
func TestRelayQuedaEntreConfirmEMarca(t *testing.T) {
	e := start(t)
	const rows = 200
	pool := e.reset(t, rows)
	ctx := context.Background()
	a, b := newRelay(t, e, "A", pool), newRelay(t, e, "B", pool)

	n, txA, err := a.batch(ctx, 50) // publica e confirma, mas ainda não marca nem commita
	if err != nil || n != 50 {
		t.Fatalf("A: n=%d err=%v", n, err)
	}
	lockedByA := map[int64]bool{}
	for _, id := range a.ids {
		lockedByA[id] = true
	}
	if err := b.run(ctx, 20); err != nil { // B drena o que não está travado
		t.Fatal(err)
	}
	for _, id := range b.ids {
		if lockedByA[id] {
			t.Fatalf("SKIP LOCKED falhou: B publicou id %d travado por A", id)
		}
	}
	t.Logf("enquanto A segura 50 linhas: B publicou %d (nenhuma das travadas)", b.count)
	txA.Rollback(ctx) // "queda" de A entre o confirm e a marca
	if err := b.run(ctx, 20); err != nil {
		t.Fatal(err)
	}
	seen := drain(t, e)
	dups, total := 0, 0
	for _, c := range seen {
		total += c
		if c > 1 {
			dups += c - 1
		}
	}
	t.Logf("depois da queda de A: ids distintos=%d de %d, mensagens=%d, duplicatas=%d (esperado 50)", len(seen), rows, total, dups)
	if len(seen) != rows {
		t.Fatalf("perda: %d de %d ids", len(seen), rows)
	}
	if dups != 50 {
		t.Errorf("duplicatas=%d, esperado 50", dups)
	}
}
