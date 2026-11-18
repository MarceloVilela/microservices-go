//go:build integration

// Testes de integração do ms-worker: o binário real (compilado com a tag faultinject, que
// libera o servidor de imagens em loopback e os pontos de kill), contra Postgres 18 e
// RabbitMQ 4 em containers. O ingest é simulado: o teste grava lote, fotos e publica as
// mensagens (o ingest já tem os seus testes).
package tests

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"image"
	"image/color"
	"image/jpeg"
	"image/png"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"sync/atomic"
	"syscall"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	amqp "github.com/rabbitmq/amqp091-go"

	"github.com/MarceloVilela/microservices-go/platform/db"
	"github.com/MarceloVilela/microservices-go/platform/messages"
	"github.com/MarceloVilela/microservices-go/platform/topology"
)

var (
	wBinOnce sync.Once
	wBinPath string
	wBinErr  error
)

func workerBinary(t *testing.T) string {
	t.Helper()
	wBinOnce.Do(func() {
		dir, err := os.MkdirTemp("", "ms-worker-bin")
		if err != nil {
			wBinErr = err
			return
		}
		wBinPath = filepath.Join(dir, "ms-worker")
		cmd := exec.Command("go", "build", "-tags", "faultinject", "-o", wBinPath, "./cmd/ms-worker")
		cmd.Dir = filepath.Join("..", "ms-worker")
		if out, err := cmd.CombinedOutput(); err != nil {
			wBinErr = fmt.Errorf("go build: %w\n%s", err, out)
		}
	})
	if wBinErr != nil {
		t.Fatal(wBinErr)
	}
	return wBinPath
}

// ---- servidor de imagens (o "Picsum" do teste) ----

func jpegBytes(w, h int) []byte {
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := range h {
		for x := range w {
			img.Set(x, y, color.RGBA{uint8(x * 255 / w), uint8(y * 255 / h), 128, 255})
		}
	}
	var b bytes.Buffer
	_ = jpeg.Encode(&b, img, &jpeg.Options{Quality: 80})
	return b.Bytes()
}

func bigPNG() []byte { // 3000x3000 = 9 Mpx, cinza uniforme (poucos KB)
	var b bytes.Buffer
	_ = png.Encode(&b, image.NewGray(image.Rect(0, 0, 3000, 3000)))
	return b.Bytes()
}

type imageServer struct {
	*httptest.Server
	flakyHits atomic.Int32
}

func newImageServer(t *testing.T) *imageServer {
	t.Helper()
	good, big := jpegBytes(1200, 800), bigPNG()
	s := &imageServer{}
	mux := http.NewServeMux()
	mux.HandleFunc("/ok/", func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write(good) })
	mux.HandleFunc("/missing", func(w http.ResponseWriter, _ *http.Request) { http.NotFound(w, nil) })
	mux.HandleFunc("/big.png", func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write(big) })
	mux.HandleFunc("/garbage", func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte("não é imagem")) })
	mux.HandleFunc("/flaky-once", func(w http.ResponseWriter, _ *http.Request) {
		if s.flakyHits.Add(1) == 1 {
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		_, _ = w.Write(good)
	})
	s.Server = httptest.NewServer(mux)
	t.Cleanup(s.Close)
	return s
}

// ---- worker como processo ----

type workerProc struct {
	base string
	cmd  *exec.Cmd
	out  *bytes.Buffer
	done chan struct{}
}

func startWorker(t *testing.T, amqpURL, dbURL, thumbs, allowedHost string, extra ...string) *workerProc {
	t.Helper()
	port := freePort(t)
	cmd := exec.Command(workerBinary(t))
	cmd.Env = append(os.Environ(),
		"AMQP_URL="+amqpURL, "DATABASE_URL="+dbURL, "HTTP_ADDR=127.0.0.1:"+port, "THUMBS_DIR="+thumbs,
		"ALLOWED_HOSTS="+allowedHost, "WORKER_TEST_RELAX_FETCH=1", "READY_CACHE_TTL=100ms",
		"FETCH_TIMEOUT=5s", "SHUTDOWN_TIMEOUT=5s", "LOG_LEVEL=debug")
	cmd.Env = append(cmd.Env, extra...)
	out := &bytes.Buffer{}
	cmd.Stderr, cmd.Stdout = &syncWriter{w: out}, &syncWriter{w: out}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	w := &workerProc{base: "http://127.0.0.1:" + port, cmd: cmd, out: out, done: make(chan struct{})}
	go func() { _ = cmd.Wait(); close(w.done) }()
	t.Cleanup(func() {
		select {
		case <-w.done:
		default:
			_ = cmd.Process.Signal(syscall.SIGTERM)
			select {
			case <-w.done:
			case <-time.After(15 * time.Second):
				_ = cmd.Process.Kill()
				<-w.done
			}
		}
		if t.Failed() {
			t.Logf("log do ms-worker (%s):\n%s", w.base, out.String())
		}
	})
	return w
}

func (w *workerProc) status(path string) int {
	resp, err := http.Get(w.base + path)
	if err != nil {
		return 0
	}
	defer resp.Body.Close()
	return resp.StatusCode
}

func (w *workerProc) waitReady(t *testing.T) {
	t.Helper()
	eventually(t, 30*time.Second, "ms-worker /readyz", func() bool { return w.status("/readyz") == 200 })
}

// waitExit espera o processo morrer (kill injetado).
func (w *workerProc) waitExit(t *testing.T) {
	t.Helper()
	select {
	case <-w.done:
	case <-time.After(30 * time.Second):
		t.Fatal("o worker não morreu no ponto de falha")
	}
}

// ---- dados ----

// seed grava lote e fotos (como o ingest faria) e devolve as mensagens photo.process, na ordem.
func seed(t *testing.T, p *pgxpool.Pool, urls ...string) (string, []string, [][]byte) {
	t.Helper()
	ctx := context.Background()
	batchID := messages.NewID()
	if _, err := p.Exec(ctx, `INSERT INTO batches (id, total) VALUES ($1, $2)`, batchID, len(urls)); err != nil {
		t.Fatal(err)
	}
	var ids []string
	var bodies [][]byte
	for _, u := range urls {
		id := messages.NewID()
		if _, err := p.Exec(ctx, `INSERT INTO photos (id, batch_id, source_url) VALUES ($1, $2, $3)`, id, batchID, u); err != nil {
			t.Fatal(err)
		}
		b, _ := json.Marshal(messages.PhotoProcess{V: messages.Version, MessageID: messages.NewID(), BatchID: batchID, PhotoID: id, SourceURL: u})
		ids, bodies = append(ids, id), append(bodies, b)
	}
	return batchID, ids, bodies
}

func publish(t *testing.T, url string, bodies ...[]byte) {
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
	if err := ch.Confirm(false); err != nil {
		t.Fatal(err)
	}
	for _, b := range bodies {
		var m messages.PhotoProcess
		_ = json.Unmarshal(b, &m)
		conf, err := ch.PublishWithDeferredConfirmWithContext(context.Background(), topology.Exchange, topology.PhotoProcess.Key, true, false,
			amqp.Publishing{ContentType: "application/json", DeliveryMode: amqp.Persistent, MessageId: m.MessageID, Body: b})
		if err != nil {
			t.Fatal(err)
		}
		if ok, err := conf.WaitContext(context.Background()); err != nil || !ok {
			t.Fatalf("publicar: ok=%v err=%v", ok, err)
		}
	}
}

// migrated devolve um banco novo, já migrado (o dono do esquema é o ingest; aqui o teste o substitui).
func migrated(t *testing.T) (string, *pgxpool.Pool) {
	t.Helper()
	url := freshDB(t)
	p := pool(t, url)
	if err := db.Migrate(context.Background(), p); err != nil {
		t.Fatal(err)
	}
	return url, p
}

func photoStatus(t *testing.T, p *pgxpool.Pool, id string) string {
	t.Helper()
	var s string
	if err := p.QueryRow(context.Background(), `SELECT status FROM photos WHERE id = $1`, id).Scan(&s); err != nil {
		t.Fatal(err)
	}
	return s
}

func batchDone(t *testing.T, p *pgxpool.Pool, id string) bool {
	return count(t, p, `SELECT count(*) FROM batches WHERE id = $1 AND completed_at IS NOT NULL`, id) == 1
}

// thumbSize decodifica a miniatura e devolve largura e altura.
func thumbSize(t *testing.T, dir, photoID string) (int, int) {
	t.Helper()
	f, err := os.Open(filepath.Join(dir, photoID+".jpg"))
	if err != nil {
		t.Fatalf("miniatura ausente: %v", err)
	}
	defer f.Close()
	img, err := jpeg.Decode(f)
	if err != nil {
		t.Fatalf("miniatura truncada ou inválida: %v", err)
	}
	return img.Bounds().Dx(), img.Bounds().Dy()
}

func noThumb(t *testing.T, dir, photoID string) {
	t.Helper()
	if _, err := os.Stat(filepath.Join(dir, photoID+".jpg")); err == nil {
		t.Errorf("não deveria existir miniatura para %s", photoID)
	}
}

func completedMessages(t *testing.T, p *pgxpool.Pool, batchID string) []messages.BatchCompleted {
	t.Helper()
	rows, err := p.Query(context.Background(), `SELECT payload::text FROM outbox WHERE routing_key = 'batch.completed' AND payload->>'batch_id' = $1`, batchID)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var out []messages.BatchCompleted
	for rows.Next() {
		var s string
		if err := rows.Scan(&s); err != nil {
			t.Fatal(err)
		}
		var m messages.BatchCompleted
		if err := json.Unmarshal([]byte(s), &m); err != nil {
			t.Fatal(err)
		}
		out = append(out, m)
	}
	return out
}

// ---- testes ----

func TestWorkerProcessaLoteComSucessoEFalhasDefinitivas(t *testing.T) {
	url := setupBroker(t)
	dbURL, p := migrated(t)
	srv, thumbs := newImageServer(t), t.TempDir()
	host := "127.0.0.1"
	batchID, ids, bodies := seed(t, p,
		srv.URL+"/ok/1", srv.URL+"/ok/2", srv.URL+"/ok/3",
		srv.URL+"/missing",      // 404: definitiva
		srv.URL+"/big.png",      // pixels acima do teto: definitiva
		srv.URL+"/garbage",      // imagem inválida: definitiva
		"https://example.com/x", // host fora da allowlist (mensagem forjada, caso 8b)
	)
	publish(t, url, bodies...)
	w := startWorker(t, url, dbURL, thumbs, host, "MAX_PIXELS=2000000")
	w.waitReady(t)

	eventually(t, 30*time.Second, "lote terminado", func() bool { return batchDone(t, p, batchID) })

	for i := range 3 {
		if st := photoStatus(t, p, ids[i]); st != "done" {
			t.Errorf("foto %d: %s", i, st)
		}
		if w, h := thumbSize(t, thumbs, ids[i]); w != 200 || h != 133 {
			t.Errorf("miniatura %d: %dx%d, esperado 200x133", i, w, h)
		}
	}
	wantReason := map[int]string{3: "HTTP 404", 4: "pixels acima do teto", 5: "imagem inválida", 6: "host fora da allowlist"}
	for i, reason := range wantReason {
		if st := photoStatus(t, p, ids[i]); st != "failed" {
			t.Errorf("foto %d: %s", i, st)
		}
		noThumb(t, thumbs, ids[i])
		var got string
		_ = p.QueryRow(context.Background(), `SELECT failure_reason FROM photos WHERE id = $1`, ids[i]).Scan(&got)
		if got != reason {
			t.Errorf("foto %d: causa %q, esperado %q", i, got, reason)
		}
	}
	// lote com falha termina e emite batch.completed exatamente uma vez
	bc := completedMessages(t, p, batchID)
	if len(bc) != 1 || bc[0].Total != 7 || bc[0].Failed != 4 || bc[0].MessageID == "" {
		t.Errorf("batch.completed = %+v", bc)
	}
	if n := count(t, p, `SELECT completed_count FROM batches WHERE id = $1`, batchID); n != 7 {
		t.Errorf("completed_count = %d", n)
	}
	for typ, want := range map[string]int{"photo.done": 3, "photo.failed": 4, "batch.completed": 1} {
		if n := count(t, p, `SELECT count(*) FROM events WHERE batch_id = $1 AND type = $2`, batchID, typ); n != want {
			t.Errorf("eventos %s = %d, esperado %d", typ, n, want)
		}
	}
	// DLQ: as 4 falhas definitivas, com a causa nos cabeçalhos
	dlq := drain(t, url, topology.PhotoDLQ.Name)
	if len(dlq) != 4 {
		t.Fatalf("DLQ = %d mensagens, esperado 4", len(dlq))
	}
	for _, d := range dlq {
		if d.Headers[messages.HeaderFailureClass] != "permanent" || d.Headers[messages.HeaderFailureReason] == "" {
			t.Errorf("DLQ sem causa: %v", d.Headers)
		}
		_ = d.Nack(false, true)
	}
	if n := queueMessages(t, url, topology.PhotoProcess.Name); n != 0 {
		t.Errorf("photo.process ainda tem %d", n)
	}
}

// Mata o worker em cada ponto da ordem de operações: a mensagem volta, nada é duplicado
// e nenhuma miniatura fica pela metade.
func TestWorkerKillEmCadaPonto(t *testing.T) {
	for _, point := range []string{"after_tmp", "after_rename", "after_commit"} {
		t.Run(point, func(t *testing.T) {
			url := setupBroker(t)
			dbURL, p := migrated(t)
			srv, thumbs := newImageServer(t), t.TempDir()
			batchID, ids, bodies := seed(t, p, srv.URL+"/ok/1")
			publish(t, url, bodies...)

			w := startWorker(t, url, dbURL, thumbs, "127.0.0.1", "WORKER_TEST_KILL_AT="+point)
			w.waitExit(t)

			// estado logo após a queda
			final := filepath.Join(thumbs, ids[0]+".jpg")
			_, statErr := os.Stat(final)
			switch point {
			case "after_tmp":
				if statErr == nil {
					t.Error("miniatura definitiva não pode existir antes do rename")
				}
				if photoStatus(t, p, ids[0]) != "pending" {
					t.Error("foto deveria seguir pending")
				}
			case "after_rename":
				if statErr != nil {
					t.Error("miniatura deveria existir depois do rename")
				}
				thumbSize(t, thumbs, ids[0]) // inteira, nunca truncada
				if photoStatus(t, p, ids[0]) != "pending" {
					t.Error("foto deveria seguir pending (transação ainda não rodou)")
				}
			case "after_commit":
				if photoStatus(t, p, ids[0]) != "done" {
					t.Error("a transação já tinha sido commitada")
				}
			}
			// sem ack: o broker devolve a mensagem quando a conexão cai
			eventually(t, 15*time.Second, "mensagem reentregue (Ready)", func() bool {
				return queueMessages(t, url, topology.PhotoProcess.Name) == 1
			})

			// reinicia sem o kill: processa (ou só dá ack) e fecha o lote
			startWorker(t, url, dbURL, thumbs, "127.0.0.1")
			eventually(t, 30*time.Second, "lote terminado", func() bool { return batchDone(t, p, batchID) })
			eventually(t, 15*time.Second, "fila vazia", func() bool {
				return queueMessages(t, url, topology.PhotoProcess.Name) == 0
			})

			if photoStatus(t, p, ids[0]) != "done" {
				t.Error("foto deveria terminar done")
			}
			if w, h := thumbSize(t, thumbs, ids[0]); w != 200 || h != 133 {
				t.Errorf("miniatura %dx%d", w, h)
			}
			if n := count(t, p, `SELECT completed_count FROM batches WHERE id = $1`, batchID); n != 1 {
				t.Errorf("completed_count = %d (resultado duplicado)", n)
			}
			if n := count(t, p, `SELECT count(*) FROM events WHERE photo_id = $1 AND type = 'photo.done'`, ids[0]); n != 1 {
				t.Errorf("photo.done = %d eventos", n)
			}
			if n := len(completedMessages(t, p, batchID)); n != 1 {
				t.Errorf("batch.completed = %d", n)
			}
		})
	}
}

// Fotos de lotes diferentes (e do mesmo lote) terminando ao mesmo tempo: um batch.completed por lote.
func TestWorkerFotosTerminandoJuntasUmEventoPorLote(t *testing.T) {
	url := setupBroker(t)
	dbURL, p := migrated(t)
	srv, thumbs := newImageServer(t), t.TempDir()

	const lotes, fotos = 15, 4
	var batches []string
	var all [][]byte
	for range lotes {
		urls := make([]string, fotos)
		for i := range urls {
			urls[i] = fmt.Sprintf("%s/ok/%d", srv.URL, i)
		}
		id, _, bodies := seed(t, p, urls...)
		batches, all = append(batches, id), append(all, bodies...)
	}
	publish(t, url, all...)
	startWorker(t, url, dbURL, thumbs, "127.0.0.1", "WORKER_CONCURRENCY=8", "WORKER_PREFETCH=8").waitReady(t)

	eventually(t, 60*time.Second, "todos os lotes terminados", func() bool {
		return count(t, p, `SELECT count(*) FROM batches WHERE completed_at IS NOT NULL`) == lotes
	})
	for _, id := range batches {
		if n := len(completedMessages(t, p, id)); n != 1 {
			t.Errorf("lote %s: %d batch.completed", id, n)
		}
		if n := count(t, p, `SELECT completed_count FROM batches WHERE id = $1`, id); n != fotos {
			t.Errorf("lote %s: completed_count = %d", id, n)
		}
	}
}

func TestWorkerReentregaRepetidaTemUmSoEfeito(t *testing.T) {
	url := setupBroker(t)
	dbURL, p := migrated(t)
	srv, thumbs := newImageServer(t), t.TempDir()
	batchID, ids, bodies := seed(t, p, srv.URL+"/ok/1", srv.URL+"/ok/2")
	// a mesma mensagem da primeira foto 6 vezes (reentrega, relay duplicado...)
	dup := [][]byte{bodies[0], bodies[0], bodies[0], bodies[0], bodies[0], bodies[0], bodies[1]}
	publish(t, url, dup...)
	startWorker(t, url, dbURL, thumbs, "127.0.0.1", "WORKER_CONCURRENCY=4", "WORKER_PREFETCH=4")

	eventually(t, 30*time.Second, "lote terminado", func() bool { return batchDone(t, p, batchID) })
	eventually(t, 15*time.Second, "fila vazia", func() bool {
		return queueMessages(t, url, topology.PhotoProcess.Name) == 0
	})
	if n := count(t, p, `SELECT completed_count FROM batches WHERE id = $1`, batchID); n != 2 {
		t.Errorf("completed_count = %d, esperado 2", n)
	}
	for _, id := range ids {
		if n := count(t, p, `SELECT count(*) FROM events WHERE photo_id = $1 AND type = 'photo.done'`, id); n != 1 {
			t.Errorf("photo.done de %s = %d", id, n)
		}
		thumbSize(t, thumbs, id)
	}
	if n := len(completedMessages(t, p, batchID)); n != 1 {
		t.Errorf("batch.completed = %d", n)
	}
}

// O3 (R3), com os parâmetros do modo oráculo: concorrência 1 e prefetch 1.
func TestWorkerFIFOComConcorrenciaUm(t *testing.T) {
	url := setupBroker(t)
	dbURL, p := migrated(t)
	srv, thumbs := newImageServer(t), t.TempDir()
	var urls []string
	for i := range 6 {
		urls = append(urls, fmt.Sprintf("%s/ok/%d", srv.URL, i))
	}
	batchID, ids, bodies := seed(t, p, urls...)
	publish(t, url, bodies...)
	startWorker(t, url, dbURL, thumbs, "127.0.0.1", "WORKER_CONCURRENCY=1", "WORKER_PREFETCH=1")
	eventually(t, 30*time.Second, "lote terminado", func() bool { return batchDone(t, p, batchID) })

	rows, err := p.Query(context.Background(), `SELECT photo_id FROM events WHERE batch_id = $1 AND type = 'photo.done' ORDER BY seq`, batchID)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var got []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			t.Fatal(err)
		}
		got = append(got, id)
	}
	if fmt.Sprint(got) != fmt.Sprint(ids) {
		t.Errorf("ordem dos eventos:\n%v\nordem de publicação:\n%v", got, ids)
	}
}

// Falha transitória (503) vai ao degrau de 5 s, volta pela DLX e termina done.
func TestWorkerFalhaTransitoriaPassaPeloDegrau(t *testing.T) {
	url := setupBroker(t)
	dbURL, p := migrated(t)
	srv, thumbs := newImageServer(t), t.TempDir()
	batchID, ids, bodies := seed(t, p, srv.URL+"/flaky-once")
	publish(t, url, bodies...)
	start := time.Now()
	startWorker(t, url, dbURL, thumbs, "127.0.0.1")

	eventually(t, 45*time.Second, "lote terminado após o retry", func() bool { return batchDone(t, p, batchID) })
	if d := time.Since(start); d < 4*time.Second {
		t.Errorf("terminou em %s: deveria ter esperado o degrau de 5 s", d)
	}
	if srv.flakyHits.Load() != 2 {
		t.Errorf("tentativas no servidor = %d, esperado 2", srv.flakyHits.Load())
	}
	if photoStatus(t, p, ids[0]) != "done" {
		t.Error("foto deveria terminar done")
	}
	if n := queueMessages(t, url, topology.PhotoDLQ.Name); n != 0 {
		t.Errorf("DLQ = %d (falha transitória não vai à DLQ)", n)
	}
}

func TestWorkerReadyzFalhaSemDiretorioDeMiniaturas(t *testing.T) {
	url := setupBroker(t)
	dbURL, _ := migrated(t)
	w := startWorker(t, url, dbURL, filepath.Join(t.TempDir(), "nao-existe"), "127.0.0.1")
	eventually(t, 30*time.Second, "/healthz", func() bool { return w.status("/healthz") == 200 })
	time.Sleep(500 * time.Millisecond)
	if st := w.status("/readyz"); st != 503 {
		t.Errorf("/readyz = %d, esperado 503", st)
	}
}

func TestWorkerEsperaOEsquemaEConsomeDepois(t *testing.T) {
	url := setupBroker(t)
	dbURL := freshDB(t) // vazio: o ingest ainda não migrou
	p := pool(t, dbURL)
	thumbs := t.TempDir()
	w := startWorker(t, url, dbURL, thumbs, "127.0.0.1")
	eventually(t, 30*time.Second, "/healthz", func() bool { return w.status("/healthz") == 200 })
	if st := w.status("/readyz"); st != 503 {
		t.Errorf("/readyz sem esquema = %d, esperado 503", st)
	}
	if err := db.Migrate(context.Background(), p); err != nil {
		t.Fatal(err)
	}
	srv := newImageServer(t)
	batchID, _, bodies := seed(t, p, srv.URL+"/ok/1")
	publish(t, url, bodies...)
	eventually(t, 30*time.Second, "lote terminado depois da migração", func() bool { return batchDone(t, p, batchID) })
}
