//go:build integration

// Testes de integração do ms-api: o binário real contra Postgres 18 e RabbitMQ 4 em containers.
// O pipeline completo (worker, relay do ms-ingest) roda como processos de verdade; o ingest HTTP
// não é usado (o teste grava lote e fotos e publica, como nos testes do worker).
package tests

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/santhosh-tekuri/jsonschema/v6"

	"github.com/MarceloVilela/microservices-go/platform/messages"
)

const (
	apiToken  = "token-de-teste"
	apiOrigin = "http://localhost:4200"
)

var (
	aBinOnce sync.Once
	aBinPath string
	aBinErr  error
)

func apiBinary(t *testing.T) string {
	t.Helper()
	aBinOnce.Do(func() {
		dir, err := os.MkdirTemp("", "ms-api-bin")
		if err != nil {
			aBinErr = err
			return
		}
		aBinPath = filepath.Join(dir, "ms-api")
		cmd := exec.Command("go", "build", "-o", aBinPath, "./cmd/ms-api")
		cmd.Dir = filepath.Join("..", "ms-api")
		if out, err := cmd.CombinedOutput(); err != nil {
			aBinErr = fmt.Errorf("go build: %w\n%s", err, out)
		}
	})
	if aBinErr != nil {
		t.Fatal(aBinErr)
	}
	return aBinPath
}

type apiProc struct {
	base string
	cmd  *exec.Cmd
	out  *lockedBuf
	done chan struct{}
}

// startAPI sobe o ms-api. amqpURL pode ser um endereço morto quando o teste não precisa do /readyz.
func startAPI(t *testing.T, amqpURL, dbURL, thumbs string, extra ...string) *apiProc {
	t.Helper()
	a := launchAPI(t, amqpURL, dbURL, thumbs, extra...)
	eventually(t, 30*time.Second, "ms-api /healthz", func() bool { return a.status("/healthz", "") == 200 })
	return a
}

func launchAPI(t *testing.T, amqpURL, dbURL, thumbs string, extra ...string) *apiProc {
	t.Helper()
	port := freePort(t)
	cmd := exec.Command(apiBinary(t))
	cmd.Env = append(os.Environ(), "AMQP_URL="+amqpURL, "DATABASE_URL="+dbURL, "HTTP_ADDR=127.0.0.1:"+port,
		"THUMBS_DIR="+thumbs, "DEMO_TOKEN="+apiToken, "CORS_ORIGINS="+apiOrigin, "READY_CACHE_TTL=100ms", "LOG_LEVEL=debug")
	cmd.Env = append(cmd.Env, extra...)
	out := &lockedBuf{}
	cmd.Stderr, cmd.Stdout = out, out
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	a := &apiProc{base: "http://127.0.0.1:" + port, cmd: cmd, out: out, done: make(chan struct{})}
	go func() { _ = cmd.Wait(); close(a.done) }()
	t.Cleanup(func() {
		select {
		case <-a.done:
		default:
			_ = cmd.Process.Signal(syscall.SIGTERM)
			select {
			case <-a.done:
			case <-time.After(15 * time.Second):
				_ = cmd.Process.Kill()
				<-a.done
			}
		}
		if t.Failed() {
			t.Logf("log do ms-api (%s):\n%s", a.base, out.String())
		}
	})
	return a
}

// stop envia SIGTERM e devolve o código de saída.
func (a *apiProc) stop(t *testing.T) int {
	t.Helper()
	_ = a.cmd.Process.Signal(syscall.SIGTERM)
	select {
	case <-a.done:
	case <-time.After(20 * time.Second):
		t.Fatal("o ms-api não encerrou no prazo depois do SIGTERM")
	}
	return a.cmd.ProcessState.ExitCode()
}

func (a *apiProc) status(path, token string) int {
	req, _ := http.NewRequest(http.MethodGet, a.base+path, nil)
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return 0
	}
	defer resp.Body.Close()
	return resp.StatusCode
}

// apiReply é a resposta já lida e fechada (StatusCode e Header, como em http.Response).
type apiReply struct {
	StatusCode int
	Header     http.Header
}

// do faz uma requisição autenticada (cabeçalhos extras em pares chave, valor) e lê o corpo todo.
func (a *apiProc) do(t *testing.T, method, path string, hdr ...string) (apiReply, []byte) {
	t.Helper()
	req, _ := http.NewRequest(method, a.base+path, nil)
	req.Header.Set("Authorization", "Bearer "+apiToken)
	for i := 0; i+1 < len(hdr); i += 2 {
		req.Header.Set(hdr[i], hdr[i+1])
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var buf bytes.Buffer
	_, _ = buf.ReadFrom(resp.Body)
	return apiReply{StatusCode: resp.StatusCode, Header: resp.Header}, buf.Bytes()
}

func (a *apiProc) getJSON(t *testing.T, path string, into any) {
	t.Helper()
	resp, body := a.do(t, http.MethodGet, path)
	if resp.StatusCode != 200 {
		t.Fatalf("GET %s: %d %s", path, resp.StatusCode, body)
	}
	if err := json.Unmarshal(body, into); err != nil {
		t.Fatalf("GET %s: %v (%s)", path, err, body)
	}
}

func (a *apiProc) sseClients() int {
	req, _ := http.NewRequest(http.MethodGet, a.base+"/statusz", nil)
	req.Header.Set("Authorization", "Bearer "+apiToken)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return -1
	}
	defer resp.Body.Close()
	var v struct {
		N int `json:"sse_clients"`
	}
	if json.NewDecoder(resp.Body).Decode(&v) != nil {
		return -1
	}
	return v.N
}

// ---- cliente SSE ----

type frame struct{ id, event, data string }

type sseConn struct {
	resp   *http.Response
	frames chan frame
	seen   []frame
}

// events abre GET /events com o token; lastID vazio = sem o cabeçalho.
func (a *apiProc) events(t *testing.T, lastID string) *sseConn {
	t.Helper()
	req, _ := http.NewRequest(http.MethodGet, a.base+"/events", nil)
	req.Header.Set("Authorization", "Bearer "+apiToken)
	if lastID != "" {
		req.Header.Set("Last-Event-ID", lastID)
	}
	resp, err := (&http.Client{}).Do(req)
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != 200 {
		t.Fatalf("GET /events: %d", resp.StatusCode)
	}
	c := &sseConn{resp: resp, frames: make(chan frame, 4096)}
	t.Cleanup(func() { _ = resp.Body.Close() })
	go func() {
		defer close(c.frames)
		sc := bufio.NewScanner(resp.Body)
		sc.Buffer(make([]byte, 1<<20), 1<<20)
		var f frame
		for sc.Scan() {
			line := sc.Text()
			switch {
			case line == "":
				if f != (frame{}) {
					c.frames <- f
				}
				f = frame{}
			case strings.HasPrefix(line, ": "):
				f = frame{event: "comment", data: line[2:]}
			case strings.HasPrefix(line, "id: "):
				f.id = line[4:]
			case strings.HasPrefix(line, "event: "):
				f.event = line[7:]
			case strings.HasPrefix(line, "data: "):
				f.data = line[6:]
			}
		}
	}()
	return c
}

// until lê frames até um satisfazer pred; falha no prazo ou se o stream fechar antes.
func (c *sseConn) until(t *testing.T, d time.Duration, what string, pred func(frame) bool) frame {
	t.Helper()
	deadline := time.After(d)
	for {
		select {
		case f, ok := <-c.frames:
			if !ok {
				t.Fatalf("stream fechou esperando %s; viu %v", what, c.seen)
			}
			c.seen = append(c.seen, f)
			if pred(f) {
				return f
			}
		case <-deadline:
			t.Fatalf("tempo esgotado esperando %s; viu %v", what, c.seen)
		}
	}
}

func isEvent(typ string) func(frame) bool { return func(f frame) bool { return f.event == typ } }

// idsUntil coleta os ids de eventos até (e incluindo) o id `last`.
func (c *sseConn) idsUntil(t *testing.T, last int) []int {
	t.Helper()
	var ids []int
	c.until(t, 20*time.Second, "id "+strconv.Itoa(last), func(f frame) bool {
		if f.id == "" {
			return false
		}
		n, _ := strconv.Atoi(f.id)
		ids = append(ids, n)
		return n >= last
	})
	return ids
}

// eventSchema compila specs/events.schema.json (JSON Schema 2020-12 completo: tipos, formatos, oneOf e payload por tipo).
func eventSchema(t *testing.T) *jsonschema.Schema {
	t.Helper()
	c := jsonschema.NewCompiler()
	c.AssertFormat()
	sch, err := c.Compile(filepath.Join("..", "specs", "events.schema.json"))
	if err != nil {
		t.Fatal(err)
	}
	return sch
}

// validateEvent devolve o erro de validação do `data` de um evento contra o esquema (nil se válido).
func validateEvent(sch *jsonschema.Schema, data string) error {
	inst, err := jsonschema.UnmarshalJSON(strings.NewReader(data))
	if err != nil {
		return err
	}
	return sch.Validate(inst)
}

// checkEventSchema confere um frame contra specs/events.schema.json (G5) com validação completa, e que o
// `event:` do frame é o `type` do dado.
func checkEventSchema(t *testing.T, sch *jsonschema.Schema, f frame) {
	t.Helper()
	if err := validateEvent(sch, f.data); err != nil {
		t.Errorf("evento %s fora do esquema: %v\n%s", f.event, err, f.data)
	}
	var env struct {
		Type string `json:"type"`
	}
	_ = json.Unmarshal([]byte(f.data), &env)
	if env.Type != f.event {
		t.Errorf("type %q difere do event: %q", env.Type, f.event)
	}
}

// O validador precisa recusar: sem isso a checagem acima passaria sempre (prova por mutação do dado).
func TestEsquemaDeEventosRecusaDadoInvalido(t *testing.T) {
	sch := eventSchema(t)
	const ok = `{"seq":1,"type":"photo.done","batch_id":"4f0c2a52-6d57-4a53-9a0e-8f6f6a1b2c3d","photo_id":"4f0c2a52-6d57-4a53-9a0e-8f6f6a1b2c3e","payload":{"status":"done","failure_class":"","failure_reason":""},"created_at":"2026-11-19T12:00:00Z"}`
	if err := validateEvent(sch, ok); err != nil {
		t.Fatalf("evento válido recusado: %v", err)
	}
	for name, bad := range map[string]string{
		"seq zero":              strings.Replace(ok, `"seq":1`, `"seq":0`, 1),
		"tipo desconhecido":     strings.Replace(ok, `"photo.done"`, `"photo.exploded"`, 1),
		"payload sem status":    strings.Replace(ok, `"status":"done",`, ``, 1),
		"batch_id não é uuid":   strings.Replace(ok, `4f0c2a52-6d57-4a53-9a0e-8f6f6a1b2c3d`, `nao-e-uuid`, 1),
		"data inválida":         strings.Replace(ok, `2026-11-19T12:00:00Z`, `ontem`, 1),
		"payload de outro tipo": strings.Replace(ok, `"photo.done"`, `"batch.completed"`, 1),
	} {
		if err := validateEvent(sch, bad); err == nil {
			t.Errorf("%s: o esquema aceitou %s", name, bad)
		}
	}
}

func insertEvents(t *testing.T, p *pgxpool.Pool, n int) {
	t.Helper()
	for range n {
		if _, err := p.Exec(context.Background(), `INSERT INTO events (type, payload) VALUES ('teste.evento', '{"k":"v"}')`); err != nil {
			t.Fatal(err)
		}
	}
}

// ---- testes ----

func TestAPIAutenticacaoEOrigem(t *testing.T) {
	dbURL, _ := migrated(t)
	a := startAPI(t, "amqp://u:p@127.0.0.1:1/", dbURL, t.TempDir())
	id := messages.NewID()
	routes := []struct{ method, path string }{
		{"GET", "/batches"}, {"GET", "/batches/" + id}, {"GET", "/photos"}, {"GET", "/photos/" + id + "/thumb"},
		{"GET", "/dlq"}, {"POST", "/dlq/" + id + "/retry"}, {"GET", "/events"},
	}
	for _, r := range routes {
		for name, auth := range map[string]string{"sem token": "", "token errado": "Bearer errado", "esquema errado": "Basic " + apiToken} {
			req, _ := http.NewRequest(r.method, a.base+r.path, nil)
			if auth != "" {
				req.Header.Set("Authorization", auth)
			}
			resp, err := http.DefaultClient.Do(req)
			if err != nil {
				t.Fatal(err)
			}
			_ = resp.Body.Close()
			if resp.StatusCode != 401 {
				t.Errorf("%s %s (%s): %d, quer 401 (SSE e miniatura incluídos)", r.method, r.path, name, resp.StatusCode)
			}
		}
		// token na query NÃO vale (vazaria em log)
		req, _ := http.NewRequest(r.method, a.base+r.path+"?token="+apiToken, nil)
		resp, _ := http.DefaultClient.Do(req)
		if resp != nil {
			_ = resp.Body.Close()
			if resp.StatusCode != 401 {
				t.Errorf("%s %s com token na query: %d, quer 401", r.method, r.path, resp.StatusCode)
			}
		}
	}

	// origem fora da allowlist é recusada, mesmo com token válido (SSE incluído)
	for _, r := range []struct{ method, path string }{{"GET", "/batches"}, {"GET", "/events"}, {"POST", "/dlq/" + id + "/retry"}} {
		resp, body := a.do(t, r.method, r.path, "Origin", "http://evil.example")
		if resp.StatusCode != 403 || !strings.Contains(string(body), "origin_not_allowed") || resp.Header.Get("Access-Control-Allow-Origin") != "" {
			t.Errorf("%s %s de origem estranha: %d %s", r.method, r.path, resp.StatusCode, body)
		}
	}
	// origem permitida: cabeçalhos CORS; preflight responde sem token e libera Authorization e Last-Event-ID
	resp, _ := a.do(t, "GET", "/batches", "Origin", apiOrigin)
	if resp.StatusCode != 200 || resp.Header.Get("Access-Control-Allow-Origin") != apiOrigin {
		t.Errorf("origem permitida: %d %v", resp.StatusCode, resp.Header)
	}
	req, _ := http.NewRequest(http.MethodOptions, a.base+"/events", nil)
	req.Header.Set("Origin", apiOrigin)
	req.Header.Set("Access-Control-Request-Method", "GET")
	pre, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	_ = pre.Body.Close()
	if h := pre.Header.Get("Access-Control-Allow-Headers"); pre.StatusCode != 204 || !strings.Contains(h, "Authorization") || !strings.Contains(h, "Last-Event-ID") {
		t.Errorf("preflight de /events: %d %v", pre.StatusCode, pre.Header)
	}

	// healthz e readyz continuam abertos; sem DEMO_TOKEN o serviço nem sobe
	if a.status("/healthz", "") != 200 {
		t.Error("/healthz deveria ficar aberto")
	}
	cmd := exec.Command(apiBinary(t))
	cmd.Env = append(os.Environ(), "AMQP_URL=amqp://u:p@127.0.0.1:1/", "DATABASE_URL="+dbURL, "HTTP_ADDR=127.0.0.1:"+freePort(t))
	if err := cmd.Run(); cmd.ProcessState == nil || cmd.ProcessState.ExitCode() != 1 {
		t.Errorf("boot sem DEMO_TOKEN: %v, quer saída 1", err)
	}
}

func TestAPIPaginacaoPorCursorEstavelComInsercoesConcorrentes(t *testing.T) {
	dbURL, p := migrated(t)
	a := startAPI(t, "amqp://u:p@127.0.0.1:1/", dbURL, t.TempDir())
	ctx := context.Background()

	original := map[string]bool{}
	for i := range 30 {
		id := messages.NewID()
		if _, err := p.Exec(ctx, `INSERT INTO batches (id, total, created_at) VALUES ($1, 1, now() - make_interval(secs => $2))`, id, float64(1000-i)); err != nil {
			t.Fatal(err)
		}
		original[id] = true
	}

	type page struct {
		Items []struct {
			ID string `json:"id"`
		} `json:"items"`
		Next *string `json:"next_cursor"`
	}
	seen := map[string]bool{}
	cursor, pages := "", 0
	for {
		var pg page
		path := "/batches?limit=7"
		if cursor != "" {
			path += "&cursor=" + cursor
		}
		a.getJSON(t, path, &pg)
		pages++
		for _, it := range pg.Items {
			if seen[it.ID] {
				t.Errorf("lote %s repetido entre páginas", it.ID)
			}
			seen[it.ID] = true
		}
		// inserções concorrentes entre as páginas: aparecem no topo (mais novas que o cursor), nunca no meio
		if _, err := p.Exec(ctx, `INSERT INTO batches (id, total) VALUES ($1, 1)`, messages.NewID()); err != nil {
			t.Fatal(err)
		}
		if pg.Next == nil {
			break
		}
		cursor = *pg.Next
	}
	for id := range original {
		if !seen[id] {
			t.Errorf("lote %s original pulado pela paginação", id)
		}
	}
	if pages != 5 { // 30 itens / 7 = 5 páginas (a última com 2); as inseridas depois ficam acima do cursor
		t.Errorf("páginas = %d, quer 5", pages)
	}
}

func TestAPILeituraDeLotesFotosEDLQ(t *testing.T) {
	dbURL, p := migrated(t)
	a := startAPI(t, "amqp://u:p@127.0.0.1:1/", dbURL, t.TempDir())
	batchID, ids, _ := seed(t, p, "https://picsum.photos/id/1/10/10", "https://picsum.photos/id/2/10/10", "https://picsum.photos/id/3/10/10")
	ctx := context.Background()
	mustExec := func(q string, args ...any) {
		t.Helper()
		if _, err := p.Exec(ctx, q, args...); err != nil {
			t.Fatal(err)
		}
	}
	mustExec(`UPDATE photos SET status='done', finished_at=now(), attempts_closed=1 WHERE id=$1`, ids[0])
	mustExec(`UPDATE photos SET status='failed', failure_class='permanent', failure_reason='HTTP 404', finished_at=now(), attempts_closed=1 WHERE id=$1`, ids[1])
	mustExec(`UPDATE batches SET completed_count=2 WHERE id=$1`, batchID)

	var b struct {
		Total, CompletedCount, FailedCount int
		CompletedAt                        *string `json:"completed_at"`
	}
	resp, body := a.do(t, "GET", "/batches/"+batchID)
	if resp.StatusCode != 200 {
		t.Fatalf("GET /batches/{id}: %d %s", resp.StatusCode, body)
	}
	var raw map[string]any
	_ = json.Unmarshal(body, &raw)
	b.Total, b.CompletedCount, b.FailedCount = int(raw["total"].(float64)), int(raw["completed_count"].(float64)), int(raw["failed_count"].(float64))
	if b.Total != 3 || b.CompletedCount != 2 || b.FailedCount != 1 || raw["completed_at"] != nil {
		t.Errorf("lote = %v", raw)
	}
	if resp, _ := a.do(t, "GET", "/batches/"+messages.NewID()); resp.StatusCode != 404 {
		t.Errorf("lote inexistente: %d", resp.StatusCode)
	}

	var list struct {
		Items []map[string]any `json:"items"`
	}
	a.getJSON(t, "/photos?batch_id="+batchID, &list)
	if len(list.Items) != 3 {
		t.Errorf("fotos do lote = %d, quer 3", len(list.Items))
	}
	a.getJSON(t, "/photos?status=pending&batch_id="+batchID, &list)
	if len(list.Items) != 1 || list.Items[0]["id"] != ids[2] {
		t.Errorf("pending = %v", list.Items)
	}
	a.getJSON(t, "/dlq", &list)
	if len(list.Items) != 1 || list.Items[0]["id"] != ids[1] || list.Items[0]["failure_class"] != "permanent" || list.Items[0]["failure_reason"] != "HTTP 404" {
		t.Errorf("DLQ deveria listar só a foto failed com a causa: %v", list.Items)
	}
}

// Pipeline completo: worker de verdade grava; o SSE mostra pending → done|failed, batch.completed e
// heartbeat em silêncio; o retry da DLQ leva a foto a done sem mexer no lote.
func TestAPIPipelineSSEMiniaturaERetryDaDLQ(t *testing.T) {
	url := setupBroker(t)
	dbURL, p := migrated(t)
	thumbs := t.TempDir()
	srv := newImageServer(t)
	healed := &atomic.Bool{}
	healSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !healed.Load() {
			http.NotFound(w, r)
			return
		}
		_, _ = w.Write(jpegBytes(1200, 800))
	}))
	t.Cleanup(healSrv.Close)

	a := startAPI(t, url, dbURL, thumbs, "SSE_HEARTBEAT=300ms")
	startIngest(t, url, dbURL) // só o relay interessa: publica o outbox do retry
	startWorker(t, url, dbURL, thumbs, "127.0.0.1")
	eventually(t, 30*time.Second, "ms-api /readyz", func() bool { return a.status("/readyz", "") == 200 })

	stream := a.events(t, "")
	stream.until(t, 5*time.Second, "heartbeat em silêncio", isEvent("comment")) // ": ok" ou ": ping"
	stream.until(t, 5*time.Second, "heartbeat", func(f frame) bool { return f.event == "comment" && f.data == "ping" })

	batchID, ids, bodies := seed(t, p, srv.URL+"/ok/1", healSrv.URL+"/foto")
	publish(t, url, bodies...)

	done := stream.until(t, 30*time.Second, "photo.done", isEvent("photo.done"))
	failed := stream.until(t, 30*time.Second, "photo.failed", isEvent("photo.failed"))
	if !strings.Contains(done.data, ids[0]) || !strings.Contains(failed.data, ids[1]) || !strings.Contains(failed.data, "HTTP 404") {
		t.Errorf("eventos inesperados:\n%v\n%v", done, failed)
	}
	completed := stream.until(t, 30*time.Second, "batch.completed", isEvent("batch.completed"))
	if completed.id == "" || !strings.Contains(completed.data, batchID) {
		t.Errorf("batch.completed = %v", completed)
	}
	eventually(t, 10*time.Second, "outbox do batch.completed", func() bool { return len(completedMessages(t, p, batchID)) == 1 })

	// a DLQ mostra a causa
	var dlq struct {
		Items []map[string]any `json:"items"`
	}
	a.getJSON(t, "/dlq", &dlq)
	if len(dlq.Items) != 1 || dlq.Items[0]["id"] != ids[1] || dlq.Items[0]["failure_reason"] != "HTTP 404" {
		t.Fatalf("DLQ = %v", dlq.Items)
	}

	// miniatura: 200 com ETag, 304 com If-None-Match; só com Bearer (aceite já cobre o 401)
	resp, body := a.do(t, "GET", "/photos/"+ids[0]+"/thumb")
	etag := resp.Header.Get("ETag")
	if resp.StatusCode != 200 || etag == "" || resp.Header.Get("Content-Type") != "image/jpeg" || len(body) == 0 {
		t.Fatalf("miniatura: %d %v", resp.StatusCode, resp.Header)
	}
	if resp, _ := a.do(t, "GET", "/photos/"+ids[0]+"/thumb", "If-None-Match", etag); resp.StatusCode != 304 {
		t.Errorf("If-None-Match: %d, quer 304", resp.StatusCode)
	}
	if resp, _ := a.do(t, "GET", "/photos/"+ids[1]+"/thumb"); resp.StatusCode != 404 {
		t.Errorf("foto sem miniatura: %d, quer 404", resp.StatusCode)
	}

	// retry 1: a origem continua quebrada ⇒ volta à DLQ, e o lote não muda
	retry := func(id string) int {
		resp, _ := a.do(t, "POST", "/dlq/"+id+"/retry")
		return resp.StatusCode
	}
	if c := retry(ids[1]); c != 202 {
		t.Fatalf("retry: %d, quer 202", c)
	}
	stream.until(t, 30*time.Second, "photo.retried", isEvent("photo.retried"))
	stream.until(t, 30*time.Second, "photo.failed de novo", isEvent("photo.failed"))
	if got := count(t, p, `SELECT completed_count FROM batches WHERE id=$1`, batchID); got != 2 {
		t.Fatalf("completed_count = %d depois de falhar de novo, quer 2 (o retry não conta o lote outra vez)", got)
	}

	// retry 2: origem curada ⇒ done, miniatura gerada, lote intocado e batch.completed NÃO reemitido
	healed.Store(true)
	if c := retry(ids[1]); c != 202 {
		t.Fatalf("retry 2: %d, quer 202", c)
	}
	stream.until(t, 30*time.Second, "photo.done depois do retry", func(f frame) bool { return f.event == "photo.done" && strings.Contains(f.data, ids[1]) })
	if st := photoStatus(t, p, ids[1]); st != "done" {
		t.Errorf("status = %s, quer done", st)
	}
	if got := count(t, p, `SELECT completed_count FROM batches WHERE id=$1`, batchID); got != 2 {
		t.Errorf("completed_count = %d, quer 2", got)
	}
	if n := len(completedMessages(t, p, batchID)); n != 1 {
		t.Errorf("batch.completed no outbox = %d, quer 1 (não reemite)", n)
	}
	if resp, _ := a.do(t, "GET", "/photos/"+ids[1]+"/thumb"); resp.StatusCode != 200 {
		t.Errorf("miniatura da foto reprocessada: %d", resp.StatusCode)
	}
	a.getJSON(t, "/dlq", &dlq)
	if len(dlq.Items) != 0 {
		t.Errorf("DLQ deveria esvaziar: %v", dlq.Items)
	}

	// todo evento visto no stream respeita specs/events.schema.json (G5)
	checked := 0
	sch := eventSchema(t) // compilado uma vez
	for _, f := range stream.seen {
		if f.id != "" {
			checkEventSchema(t, sch, f)
			checked++
		}
	}
	if checked < 5 { // photo.done, photo.failed, batch.completed, photo.retried, photo.failed, photo.done
		t.Errorf("só %d eventos conferidos contra o esquema", checked)
	}

	// foto done ou inexistente: sem efeito
	if c := retry(ids[1]); c != 409 {
		t.Errorf("retry de foto done: %d, quer 409", c)
	}
	if c := retry(messages.NewID()); c != 404 {
		t.Errorf("retry de foto inexistente: %d, quer 404", c)
	}
}

// Last-Event-ID retoma sem perder nem repetir, inclusive depois de reiniciar o ms-api.
func TestAPILastEventIDDepoisDeReiniciarOMsAPI(t *testing.T) {
	dbURL, p := migrated(t)
	thumbs := t.TempDir()
	a := startAPI(t, "amqp://u:p@127.0.0.1:1/", dbURL, thumbs)
	insertEvents(t, p, 10)

	c1 := a.events(t, "0")
	if ids := c1.idsUntil(t, 6); len(ids) != 6 || ids[0] != 1 {
		t.Fatalf("1ª conexão: ids = %v", ids)
	}
	_ = c1.resp.Body.Close() // o cliente cai depois de ver o 6

	if code := a.stop(t); code != 0 {
		t.Fatalf("saída do ms-api = %d, quer 0", code)
	}
	insertEvents(t, p, 5) // eventos gravados com o ms-api fora do ar

	b := startAPI(t, "amqp://u:p@127.0.0.1:1/", dbURL, thumbs)
	c2 := b.events(t, "6")
	ids := c2.idsUntil(t, 15)
	want := []int{7, 8, 9, 10, 11, 12, 13, 14, 15}
	if fmt.Sprint(ids) != fmt.Sprint(want) {
		t.Errorf("depois de reiniciar: ids = %v, quer %v (sem perda e sem repetição)", ids, want)
	}
	insertEvents(t, p, 1) // e o ao vivo continua
	if ids := c2.idsUntil(t, 16); ids[len(ids)-1] != 16 {
		t.Errorf("evento ao vivo: %v", ids)
	}
}

func TestAPIRetencaoDosEventosEReset(t *testing.T) {
	dbURL, p := migrated(t)
	thumbs := t.TempDir()
	// um evento velho, que a poda do boot (EVENTS_RETENTION) remove
	if _, err := p.Exec(context.Background(), `INSERT INTO events (type, payload, created_at) VALUES ('velho', '{}', now() - interval '48 hours')`); err != nil {
		t.Fatal(err)
	}
	insertEvents(t, p, 10)
	a := startAPI(t, "amqp://u:p@127.0.0.1:1/", dbURL, thumbs, "EVENTS_RETENTION=24h")
	eventually(t, 10*time.Second, "poda do evento velho", func() bool {
		return count(t, p, `SELECT count(*) FROM events WHERE type='velho'`) == 0
	})
	if n := count(t, p, `SELECT count(*) FROM events`); n != 10 {
		t.Fatalf("a poda levou eventos dentro da janela: restam %d, quer 10", n)
	}

	// as primeiras linhas saem da janela: quem volta com um id antigo recebe `reset` e depois o que há
	if _, err := p.Exec(context.Background(), `DELETE FROM events WHERE seq <= (SELECT min(seq)+4 FROM events)`); err != nil {
		t.Fatal(err)
	}
	c := a.events(t, "1")
	r := c.until(t, 10*time.Second, "reset", isEvent("reset"))
	if !strings.Contains(r.data, "retention") {
		t.Errorf("reset = %v", r)
	}
	if ids := c.idsUntil(t, 11); len(ids) != 5 {
		t.Errorf("depois do reset: ids = %v, quer os 5 retidos", ids)
	}
}

// Cliente que não lê é desconectado (prazo de escrita), sem segurar o serviço.
func TestAPIClienteLentoEDesconectado(t *testing.T) {
	dbURL, p := migrated(t)
	a := startAPI(t, "amqp://u:p@127.0.0.1:1/", dbURL, t.TempDir(), "SSE_WRITE_TIMEOUT=500ms")

	conn, err := net.Dial("tcp", strings.TrimPrefix(a.base, "http://"))
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	if tcp, ok := conn.(*net.TCPConn); ok {
		_ = tcp.SetReadBuffer(4096) // janela de recepção mínima: o servidor enche o buffer logo
	}
	_, _ = fmt.Fprintf(conn, "GET /events HTTP/1.1\r\nHost: x\r\nAuthorization: Bearer %s\r\n\r\n", apiToken)
	eventually(t, 5*time.Second, "cliente lento conectado", func() bool { return a.sseClients() == 1 })

	// ~60 MB de eventos, que o cliente nunca lê
	if _, err := p.Exec(context.Background(),
		`INSERT INTO events (type, payload) SELECT 'grande', jsonb_build_object('x', repeat('a', 3000)) FROM generate_series(1, 20000)`); err != nil {
		t.Fatal(err)
	}
	eventually(t, 30*time.Second, "cliente lento desconectado", func() bool { return a.sseClients() == 0 })

	// um cliente saudável, conectado depois, continua sendo atendido
	if resp, _ := a.do(t, "GET", "/batches"); resp.StatusCode != 200 {
		t.Errorf("serviço degradado pelo cliente lento: %d", resp.StatusCode)
	}
}

func TestAPISIGTERMComStreamAbertoSaiZero(t *testing.T) {
	dbURL, _ := migrated(t)
	a := startAPI(t, "amqp://u:p@127.0.0.1:1/", dbURL, t.TempDir())
	c := a.events(t, "")
	eventually(t, 5*time.Second, "stream conectado", func() bool { return a.sseClients() == 1 })

	start := time.Now()
	if code := a.stop(t); code != 0 {
		t.Errorf("saída = %d, quer 0", code)
	}
	if d := time.Since(start); d > 8*time.Second {
		t.Errorf("shutdown levou %s com stream aberto", d)
	}
	c.until(t, 5*time.Second, "event: shutdown", isEvent("shutdown"))
}

func TestAPIReadyzFalhaSemDiretorioDeMiniaturas(t *testing.T) {
	url := setupBroker(t)
	dbURL, _ := migrated(t)
	a := startAPI(t, url, dbURL, filepath.Join(t.TempDir(), "nao-existe"))
	for range 3 {
		if c := a.status("/readyz", ""); c != 503 {
			t.Fatalf("/readyz = %d, quer 503 sem o diretório de miniaturas", c)
		}
		time.Sleep(150 * time.Millisecond)
	}
}

// O servidor compartilhado tem ReadTimeout de 15 s; o stream SSE não pode morrer por causa dele.
func TestAPIStreamSobreviveAoReadTimeoutDoServidor(t *testing.T) {
	dbURL, p := migrated(t)
	a := startAPI(t, "amqp://u:p@127.0.0.1:1/", dbURL, t.TempDir(), "SSE_HEARTBEAT=5s")
	c := a.events(t, "")
	eventually(t, 5*time.Second, "stream conectado", func() bool { return a.sseClients() == 1 })
	time.Sleep(18 * time.Second) // passa dos 15 s do ReadTimeout, só com heartbeats
	if a.sseClients() != 1 {
		t.Fatalf("o stream caiu antes dos 18 s (clientes = %d)", a.sseClients())
	}
	insertEvents(t, p, 1)
	c.until(t, 10*time.Second, "evento depois de 18 s", func(f frame) bool { return f.id != "" })
}

// Log podado por inteiro (ou vazio) com a sequência já avançada: o 1º evento novo sai na hora,
// sem esperar o GapWait nem provocar `reset` num cliente que estava em dia.
func TestAPIPrimeiroEventoDepoisDePodaTotalNaoEspera(t *testing.T) {
	dbURL, p := migrated(t)
	a := startAPI(t, "amqp://u:p@127.0.0.1:1/", dbURL, t.TempDir())
	insertEvents(t, p, 5)
	if _, err := p.Exec(context.Background(), `DELETE FROM events`); err != nil { // retenção podou tudo; a sequência segue em 5
		t.Fatal(err)
	}
	c := a.events(t, "")
	eventually(t, 5*time.Second, "stream conectado", func() bool { return a.sseClients() == 1 })
	insertEvents(t, p, 1)
	start := time.Now()
	f := c.until(t, 10*time.Second, "evento 6", func(f frame) bool { return f.id != "" })
	if f.id != "6" || time.Since(start) > 1500*time.Millisecond {
		t.Errorf("evento %s em %s; quer o 6 sem esperar o GapWait", f.id, time.Since(start))
	}
	c2 := a.events(t, "6") // cliente em dia: nada de reset
	insertEvents(t, p, 1)
	if f := c2.until(t, 10*time.Second, "evento 7", func(f frame) bool { return f.id != "" || f.event == "reset" }); f.event == "reset" {
		t.Error("cliente em dia recebeu reset")
	}
}
