//go:build integration

// Testes de integração do ms-ingest: o binário real, compilado pelo teste,
// contra Postgres 18 e RabbitMQ 4 em containers. Um proxy TCP no meio permite
// "parar o broker" sem perder o estado dele.
package tests

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	amqp "github.com/rabbitmq/amqp091-go"
	"github.com/testcontainers/testcontainers-go"
	tcpostgres "github.com/testcontainers/testcontainers-go/modules/postgres"
	"github.com/testcontainers/testcontainers-go/wait"

	"github.com/MarceloVilela/microservices-go/platform/topology"
)

var (
	pgOnce  sync.Once
	pgAdmin string
	pgErr   error
	pgCont  *tcpostgres.PostgresContainer
	dbSeq   int
	dbMu    sync.Mutex

	binOnce sync.Once
	binPath string
	binErr  error
)

// freshDB cria um banco novo no Postgres compartilhado e devolve a URL.
func freshDB(t *testing.T) string {
	t.Helper()
	pgOnce.Do(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
		defer cancel()
		pgCont, pgErr = tcpostgres.Run(ctx, "postgres:18-alpine",
			tcpostgres.WithDatabase("admin"), tcpostgres.WithUsername("app"), tcpostgres.WithPassword("app"),
			testcontainers.WithWaitStrategy(wait.ForLog("database system is ready to accept connections").WithOccurrence(2).WithStartupTimeout(2*time.Minute)))
		if pgErr == nil {
			pgAdmin, pgErr = pgCont.ConnectionString(ctx, "sslmode=disable")
		}
	})
	if pgErr != nil {
		t.Fatalf("postgres: %v", pgErr)
	}
	dbMu.Lock()
	dbSeq++
	name := fmt.Sprintf("t%d_%d", time.Now().Unix(), dbSeq)
	dbMu.Unlock()
	ctx := context.Background()
	admin, err := pgxpool.New(ctx, pgAdmin)
	if err != nil {
		t.Fatal(err)
	}
	defer admin.Close()
	if _, err := admin.Exec(ctx, "CREATE DATABASE "+name); err != nil {
		t.Fatal(err)
	}
	return strings.Replace(pgAdmin, "/admin?", "/"+name+"?", 1)
}

func pool(t *testing.T, url string) *pgxpool.Pool {
	t.Helper()
	p, err := pgxpool.New(context.Background(), url)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(p.Close)
	return p
}

func count(t *testing.T, p *pgxpool.Pool, query string, args ...any) int {
	t.Helper()
	var n int
	if err := p.QueryRow(context.Background(), query, args...).Scan(&n); err != nil {
		t.Fatalf("%s: %v", query, err)
	}
	return n
}

// ---- proxy TCP ----

type proxy struct {
	target string
	addr   string
	mu     sync.Mutex
	ln     net.Listener
	conns  []net.Conn
}

func newProxy(t *testing.T, target string) *proxy {
	t.Helper()
	p := &proxy{target: target}
	// reserva a porta uma vez: o serviço recebe sempre o mesmo endereço
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	p.addr = ln.Addr().String()
	_ = ln.Close()
	p.Start(t)
	t.Cleanup(p.Stop)
	return p
}

func (p *proxy) Start(t *testing.T) {
	t.Helper()
	var ln net.Listener
	var err error
	for range 50 { // a porta pode demorar a ser liberada
		if ln, err = net.Listen("tcp", p.addr); err == nil {
			break
		}
		time.Sleep(100 * time.Millisecond)
	}
	if err != nil {
		t.Fatal(err)
	}
	p.mu.Lock()
	p.ln = ln
	p.mu.Unlock()
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			up, err := net.Dial("tcp", p.target)
			if err != nil {
				_ = c.Close()
				continue
			}
			p.mu.Lock()
			p.conns = append(p.conns, c, up)
			p.mu.Unlock()
			go func() { _, _ = io.Copy(up, c); _ = up.Close() }()
			go func() { _, _ = io.Copy(c, up); _ = c.Close() }()
		}
	}()
}

// Stop fecha o listener e todas as conexões: para o cliente, o broker caiu.
func (p *proxy) Stop() {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.ln != nil {
		_ = p.ln.Close()
		p.ln = nil
	}
	for _, c := range p.conns {
		_ = c.Close()
	}
	p.conns = nil
}

// ---- binário do ms-ingest ----

func ingestBinary(t *testing.T) string {
	t.Helper()
	binOnce.Do(func() {
		dir, err := os.MkdirTemp("", "ms-ingest-bin")
		if err != nil {
			binErr = err
			return
		}
		binPath = filepath.Join(dir, "ms-ingest")
		cmd := exec.Command("go", "build", "-o", binPath, "./cmd/ms-ingest")
		cmd.Dir = filepath.Join("..", "ms-ingest")
		if out, err := cmd.CombinedOutput(); err != nil {
			binErr = fmt.Errorf("go build: %w\n%s", err, out)
		}
	})
	if binErr != nil {
		t.Fatal(binErr)
	}
	return binPath
}

type ingest struct {
	base string
	cmd  *exec.Cmd
	out  *bytes.Buffer
}

func freePort(t *testing.T) string {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	return fmt.Sprint(ln.Addr().(*net.TCPAddr).Port)
}

func startIngest(t *testing.T, amqpURL, dbURL string, extra ...string) *ingest {
	t.Helper()
	port := freePort(t)
	cmd := exec.Command(ingestBinary(t))
	cmd.Env = append(os.Environ(),
		"AMQP_URL="+amqpURL, "DATABASE_URL="+dbURL, "HTTP_ADDR=127.0.0.1:"+port,
		"READY_CACHE_TTL=100ms", "RELAY_POLL_INTERVAL=50ms", "LOG_LEVEL=debug")
	cmd.Env = append(cmd.Env, extra...)
	out := &bytes.Buffer{}
	cmd.Stderr, cmd.Stdout = &syncWriter{w: out}, &syncWriter{w: out}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	in := &ingest{base: "http://127.0.0.1:" + port, cmd: cmd, out: out}
	t.Cleanup(func() {
		_ = cmd.Process.Signal(syscall.SIGTERM)
		done := make(chan struct{})
		go func() { _ = cmd.Wait(); close(done) }()
		select {
		case <-done:
		case <-time.After(15 * time.Second):
			_ = cmd.Process.Kill()
			<-done
		}
		if t.Failed() {
			t.Logf("log do ms-ingest (%s):\n%s", in.base, out.String())
		}
	})
	eventually(t, 30*time.Second, "ms-ingest /healthz", func() bool { return in.status("/healthz") == 200 })
	return in
}

type syncWriter struct {
	mu sync.Mutex
	w  io.Writer
}

func (s *syncWriter) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.w.Write(p)
}

func (in *ingest) status(path string) int {
	resp, err := http.Get(in.base + path)
	if err != nil {
		return 0
	}
	defer resp.Body.Close()
	return resp.StatusCode
}

func (in *ingest) post(t *testing.T, key, body string) (int, map[string]any, http.Header) {
	t.Helper()
	req, _ := http.NewRequest(http.MethodPost, in.base+"/batches", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	if key != "" {
		req.Header.Set("Idempotency-Key", key)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var m map[string]any
	_ = json.NewDecoder(resp.Body).Decode(&m)
	return resp.StatusCode, m, resp.Header
}

func eventually(t *testing.T, d time.Duration, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(d)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(100 * time.Millisecond)
	}
	t.Fatalf("tempo esgotado (%s): %s", d, what)
}

func photos(n int) string {
	var items []string
	for i := range n {
		items = append(items, fmt.Sprintf(`{"source_url":"https://picsum.photos/id/%d/1200/800"}`, i+1))
	}
	return `{"photos":[` + strings.Join(items, ",") + `]}`
}

// setupBroker deixa o broker compartilhado com a topologia limpa e completa.
func setupBroker(t *testing.T) string {
	t.Helper()
	url := broker(t)
	reset(t, url)
	if err := declare(t, url); err != nil {
		t.Fatal(err)
	}
	return url
}

func queueMessages(t *testing.T, url, queue string) int {
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
	q, err := ch.QueueDeclarePassive(queue, true, false, false, false, nil)
	if err != nil {
		t.Fatal(err)
	}
	return q.Messages
}

// drain lê todas as mensagens da fila (com ack manual, devolvendo no fim com Nack requeue).
func drain(t *testing.T, url, queue string) []amqp.Delivery {
	t.Helper()
	conn, err := amqp.Dial(url)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	ch, _ := conn.Channel()
	var out []amqp.Delivery
	for {
		d, ok, err := ch.Get(queue, false)
		if err != nil {
			t.Fatal(err)
		}
		if !ok {
			return out
		}
		out = append(out, d)
	}
}

// ---- testes ----

func TestIngestGravaTudoENaFilaPersistente(t *testing.T) {
	url, dbURL := setupBroker(t), freshDB(t)
	in := startIngest(t, url, dbURL)
	p := pool(t, dbURL)

	status, body, _ := in.post(t, "", photos(3))
	if status != 202 || body["total"] != float64(3) {
		t.Fatalf("POST = %d %v", status, body)
	}
	batchID, _ := body["batch_id"].(string)

	if n := count(t, p, "SELECT count(*) FROM photos WHERE batch_id = $1 AND status = 'pending'", batchID); n != 3 {
		t.Errorf("fotos = %d", n)
	}
	if n := count(t, p, "SELECT count(*) FROM events WHERE batch_id = $1 AND type = 'batch.created'", batchID); n != 1 {
		t.Errorf("eventos = %d", n)
	}
	eventually(t, 15*time.Second, "outbox publicado", func() bool {
		return count(t, p, "SELECT count(*) FROM outbox WHERE published_at IS NULL") == 0
	})
	if n := count(t, p, "SELECT count(*) FROM outbox"); n != 3 {
		t.Errorf("outbox = %d", n)
	}

	msgs := drain(t, url, topology.PhotoProcess.Name)
	if len(msgs) != 3 {
		t.Fatalf("mensagens na fila = %d", len(msgs))
	}
	for _, d := range msgs {
		var m map[string]any
		if err := json.Unmarshal(d.Body, &m); err != nil {
			t.Fatal(err)
		}
		if d.DeliveryMode != amqp.Persistent || m["v"] != float64(1) || m["batch_id"] != batchID || m["photo_id"] == "" || d.MessageId == "" {
			t.Errorf("mensagem fora do contrato: %s (modo %d, id %q)", d.Body, d.DeliveryMode, d.MessageId)
		}
		_ = d.Nack(false, true)
	}
}

func TestIngestEntradaInvalidaNaoTocaOBanco(t *testing.T) {
	url, dbURL := setupBroker(t), freshDB(t)
	in := startIngest(t, url, dbURL)
	p := pool(t, dbURL)

	bodies := map[string]string{
		"host fora da allowlist": `{"photos":[{"source_url":"https://example.com/foto.jpg"}]}`,
		"lista vazia":            `{"photos":[]}`,
		"url inválida":           `{"photos":[{"source_url":"nao-e-url"}]}`,
		"json quebrado":          `{"photos":`,
	}
	for name, b := range bodies {
		if status, _, _ := in.post(t, "", b); status != 400 {
			t.Errorf("%s: status %d, quer 400", name, status)
		}
	}
	for _, tbl := range []string{"batches", "photos", "outbox", "events"} {
		if n := count(t, p, "SELECT count(*) FROM "+tbl); n != 0 {
			t.Errorf("%s tem %d linhas depois de só entradas inválidas", tbl, n)
		}
	}
}

func TestIngestIdempotencyKey(t *testing.T) {
	url, dbURL := setupBroker(t), freshDB(t)
	in := startIngest(t, url, dbURL)
	p := pool(t, dbURL)

	s1, b1, _ := in.post(t, "chave-1", photos(2))
	s2, b2, h2 := in.post(t, "chave-1", photos(2))
	if s1 != 202 || s2 != 200 || b1["batch_id"] != b2["batch_id"] || h2.Get("Idempotent-Replay") != "true" {
		t.Fatalf("1ª %d %v; 2ª %d %v", s1, b1, s2, b2)
	}
	if s, _, _ := in.post(t, "chave-1", photos(3)); s != 422 {
		t.Errorf("mesma chave, outro corpo = %d, quer 422", s)
	}
	if n := count(t, p, "SELECT count(*) FROM batches"); n != 1 {
		t.Errorf("lotes = %d, quer 1", n)
	}
	if n := count(t, p, "SELECT count(*) FROM outbox"); n != 2 {
		t.Errorf("outbox = %d, quer 2", n)
	}
	if s, _, _ := in.post(t, "chave inválida!", photos(1)); s != 400 {
		t.Errorf("chave inválida = %d, quer 400", s)
	}
}

func TestIngestIdempotencyKeyConcorrente(t *testing.T) {
	url, dbURL := setupBroker(t), freshDB(t)
	in := startIngest(t, url, dbURL)
	p := pool(t, dbURL)

	const n = 20
	var wg sync.WaitGroup
	statuses := make(chan int, n)
	ids := make(chan string, n)
	for range n {
		wg.Add(1)
		go func() {
			defer wg.Done()
			s, b, _ := in.post(t, "corrida", photos(5))
			statuses <- s
			id, _ := b["batch_id"].(string)
			ids <- id
		}()
	}
	wg.Wait()
	close(statuses)
	close(ids)
	got := map[int]int{}
	for s := range statuses {
		got[s]++
	}
	if got[202] != 1 || got[200] != n-1 {
		t.Errorf("status = %v, quer 1×202 e %d×200", got, n-1)
	}
	seen := map[string]bool{}
	for id := range ids {
		seen[id] = true
	}
	if len(seen) != 1 {
		t.Errorf("batch_ids distintos = %d, quer 1", len(seen))
	}
	if c := count(t, p, "SELECT count(*) FROM batches"); c != 1 {
		t.Errorf("lotes = %d", c)
	}
	if c := count(t, p, "SELECT count(*) FROM outbox"); c != 5 {
		t.Errorf("outbox = %d, quer 5", c)
	}
}

// RQ2 e RQ5: com o broker fora, o POST responde 202 sem esperar, as linhas ficam
// no outbox e o /readyz falha; ao voltar, o relay publica todas as pendentes.
func TestIngestBrokerParadoDepoisVolta(t *testing.T) {
	realURL, dbURL := setupBroker(t), freshDB(t)
	conn, err := amqp.ParseURI(realURL)
	if err != nil {
		t.Fatal(err)
	}
	px := newProxy(t, net.JoinHostPort(conn.Host, fmt.Sprint(conn.Port)))
	viaProxy := fmt.Sprintf("amqp://%s:%s@%s/", conn.Username, conn.Password, px.addr)

	in := startIngest(t, viaProxy, dbURL)
	p := pool(t, dbURL)
	eventually(t, 15*time.Second, "/readyz 200 com tudo no ar", func() bool { return in.status("/readyz") == 200 })

	// um lote antes da queda, já publicado
	if s, _, _ := in.post(t, "", photos(2)); s != 202 {
		t.Fatalf("POST antes da queda = %d", s)
	}
	eventually(t, 15*time.Second, "lote 1 publicado", func() bool { return queueMessages(t, realURL, topology.PhotoProcess.Name) == 2 })

	px.Stop() // o broker "cai"
	eventually(t, 15*time.Second, "/readyz 503 sem broker", func() bool { return in.status("/readyz") == 503 })

	start := time.Now()
	s, body, _ := in.post(t, "", photos(7))
	if s != 202 {
		t.Fatalf("POST com broker parado = %d, quer 202", s)
	}
	if d := time.Since(start); d > 2*time.Second {
		t.Errorf("POST demorou %s com o broker parado (não deveria esperar o broker)", d)
	}
	batch2, _ := body["batch_id"].(string)
	if n := count(t, p, "SELECT count(*) FROM outbox WHERE published_at IS NULL"); n != 7 {
		t.Errorf("pendentes no outbox = %d, quer 7", n)
	}

	time.Sleep(1500 * time.Millisecond) // o relay tenta e falha; nada pode ser marcado
	if n := count(t, p, "SELECT count(*) FROM outbox WHERE published_at IS NULL"); n != 7 {
		t.Errorf("pendentes com broker parado = %d, quer 7 (nada deve ser marcado sem confirm)", n)
	}

	px.Start(t) // o broker volta
	eventually(t, 60*time.Second, "relay publica todas as pendentes", func() bool {
		return count(t, p, "SELECT count(*) FROM outbox WHERE published_at IS NULL") == 0
	})
	eventually(t, 15*time.Second, "/readyz volta a 200", func() bool { return in.status("/readyz") == 200 })

	msgs := drain(t, realURL, topology.PhotoProcess.Name)
	byBatch := map[string]int{}
	photoIDs := map[string]bool{}
	for _, d := range msgs {
		var m map[string]string
		_ = json.Unmarshal(d.Body, &m)
		byBatch[m["batch_id"]]++
		photoIDs[m["photo_id"]] = true
		_ = d.Nack(false, true)
	}
	if len(msgs) < 9 || byBatch[batch2] != 7 || len(photoIDs) != 9 {
		t.Errorf("fila: %d mensagens, %d do lote 2 (quer 7), %d fotos distintas (quer 9)", len(msgs), byBatch[batch2], len(photoIDs))
	}
	if len(byBatch) != 2 {
		t.Errorf("mensagens de %d lotes, quer 2 (nenhuma fora do lote)", len(byBatch))
	}
}

// Duas instâncias do ms-ingest (dois relays) no mesmo banco: SKIP LOCKED divide
// o trabalho; nenhuma foto some e as duplicatas, se houver, são raras (at-least-once).
func TestIngestDoisRelaysNaoPerdemNada(t *testing.T) {
	url, dbURL := setupBroker(t), freshDB(t)
	a := startIngest(t, url, dbURL)
	b := startIngest(t, url, dbURL)
	p := pool(t, dbURL)

	const lotes = 6
	var wg sync.WaitGroup
	for i := range lotes {
		wg.Add(1)
		go func() {
			defer wg.Done()
			target := a
			if i%2 == 1 {
				target = b
			}
			if s, _, _ := target.post(t, "", photos(100)); s != 202 {
				t.Errorf("POST %d = %d", i, s)
			}
		}()
	}
	wg.Wait()

	eventually(t, 60*time.Second, "600 linhas publicadas", func() bool {
		return count(t, p, "SELECT count(*) FROM outbox WHERE published_at IS NULL") == 0
	})
	msgs := drain(t, url, topology.PhotoProcess.Name)
	photoIDs := map[string]bool{}
	for _, d := range msgs {
		var m map[string]string
		_ = json.Unmarshal(d.Body, &m)
		photoIDs[m["photo_id"]] = true
		_ = d.Nack(false, true)
	}
	if len(photoIDs) != lotes*100 {
		t.Errorf("fotos distintas na fila = %d, quer %d (nenhuma perdida)", len(photoIDs), lotes*100)
	}
	if len(msgs) != lotes*100 {
		t.Logf("aviso: %d mensagens para %d fotos (duplicatas são admitidas pelo at-least-once)", len(msgs), lotes*100)
	}
}

func TestIngestCORSETokenNoPreflight(t *testing.T) {
	url, dbURL := setupBroker(t), freshDB(t)
	in := startIngest(t, url, dbURL, "CORS_ORIGINS=http://localhost:4200", "DEMO_TOKEN=segredo-de-teste")

	req, _ := http.NewRequest(http.MethodOptions, in.base+"/batches", nil)
	req.Header.Set("Origin", "http://localhost:4200")
	req.Header.Set("Access-Control-Request-Method", "POST")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != 204 || resp.Header.Get("Access-Control-Allow-Origin") != "http://localhost:4200" {
		t.Errorf("preflight = %d %v", resp.StatusCode, resp.Header)
	}

	if s, _, _ := in.post(t, "", photos(1)); s != 401 {
		t.Errorf("POST sem token = %d, quer 401", s)
	}
	req, _ = http.NewRequest(http.MethodPost, in.base+"/batches", strings.NewReader(photos(1)))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer segredo-de-teste")
	req.Header.Set("Origin", "http://localhost:4200")
	resp, err = http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != 202 || resp.Header.Get("Access-Control-Allow-Origin") != "http://localhost:4200" {
		t.Errorf("POST com token = %d %v", resp.StatusCode, resp.Header)
	}
}
