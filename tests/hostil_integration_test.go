//go:build integration

// Fase 7, bloco C (N4, ADR 0010): entrada hostil de ponta a ponta. Cada caso confere a classificação
// (400, falha definitiva ou transitória), e que o worker segue consumindo depois de todos.
package tests

import (
	"bytes"
	"context"
	"fmt"
	"image"
	"image/png"
	"net/http"
	"net/http/httptest"
	"os"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/MarceloVilela/microservices-go/platform/topology"
)

// hugeDimsPNG é um PNG que declara 6000x6000 RGBA (36 Mpx, acima do teto de 25 Mpx; 144 MB se
// fosse decodificado) e pesa poucas dezenas de KB.
func hugeDimsPNG() []byte {
	var b bytes.Buffer
	_ = png.Encode(&b, image.NewRGBA(image.Rect(0, 0, 6000, 6000)))
	return b.Bytes()
}

// vmHWM lê o pico de memória residente (kB) do processo em /proc.
func vmHWM(t *testing.T, pid int) int {
	t.Helper()
	b, err := os.ReadFile(fmt.Sprintf("/proc/%d/status", pid))
	if err != nil {
		t.Skipf("sem /proc: %v", err)
	}
	for _, l := range strings.Split(string(b), "\n") {
		if f := strings.Fields(l); len(f) >= 2 && f[0] == "VmHWM:" {
			n, _ := strconv.Atoi(f[1])
			return n
		}
	}
	t.Fatal("VmHWM ausente")
	return 0
}

type hostileServer struct {
	*httptest.Server
	slowHits atomic.Int32
}

func newHostileServer(t *testing.T) *hostileServer {
	t.Helper()
	s := &hostileServer{}
	good, huge := jpegBytes(1200, 800), hugeDimsPNG()
	mux := http.NewServeMux()
	mux.HandleFunc("/ok/", func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write(good) })
	// H3: redirect para o endpoint de metadados da nuvem (host interno, fora da allowlist)
	mux.HandleFunc("/to-metadata", func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "http://169.254.169.254/latest/meta-data/", http.StatusFound)
	})
	// H4: redirect em laço (mais de 3 saltos)
	mux.HandleFunc("/loop", func(w http.ResponseWriter, r *http.Request) { http.Redirect(w, r, "/loop", http.StatusFound) })
	// H6: resposta mais lenta que FETCH_TIMEOUT
	mux.HandleFunc("/slow", func(w http.ResponseWriter, _ *http.Request) {
		s.slowHits.Add(1)
		time.Sleep(2 * time.Second)
		_, _ = w.Write(good)
	})
	// H7: corpo muito acima do limite (sem Content-Length, em pedaços)
	mux.HandleFunc("/endless", func(w http.ResponseWriter, r *http.Request) {
		chunk := make([]byte, 64<<10)
		for i := 0; i < 160 && r.Context().Err() == nil; i++ { // 10 MiB
			if _, err := w.Write(chunk); err != nil {
				return
			}
			w.(http.Flusher).Flush()
		}
	})
	// H8: dimensões enormes em arquivo pequeno
	mux.HandleFunc("/huge-dims.png", func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write(huge) })
	// H9: HTML dizendo ser imagem
	mux.HandleFunc("/html", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "image/jpeg")
		_, _ = w.Write([]byte("<html><script>alert(1)</script></html>"))
	})
	s.Server = httptest.NewServer(mux)
	t.Cleanup(s.Close)
	return s
}

func failureOf(t *testing.T, p *pgxpool.Pool, id string) (status string, class, reason *string) {
	t.Helper()
	if err := p.QueryRow(context.Background(), `SELECT status, failure_class, failure_reason FROM photos WHERE id=$1`, id).Scan(&status, &class, &reason); err != nil {
		t.Fatal(err)
	}
	return
}

// H2 a H4 e H6 a H9: cada URL termina na classificação esperada, o worker segue
// vivo, a foto válida do mesmo lote termina done, e a memória do processo não cresce com a imagem gigante.
func TestHostilWorkerClassificaCadaCaso(t *testing.T) {
	url := setupBroker(t)
	fastRetry(t, 200)
	dbURL, p := migrated(t)
	srv, thumbs := newHostileServer(t), t.TempDir()
	// FETCH_TIMEOUT curto torna /slow transitória; MAX_IMAGE_BYTES de 1 MiB corta o /endless
	w := startWorker(t, url, dbURL, thumbs, "127.0.0.1", "FETCH_TIMEOUT=700ms", "MAX_IMAGE_BYTES=1048576")

	// aquecimento: uma foto boa, para o pico de memória de referência já incluir o caminho normal
	_, warm, warmBodies := seed(t, p, srv.URL+"/ok/warm")
	publish(t, url, warmBodies...)
	eventually(t, 30*time.Second, "aquecimento", func() bool { return photoStatus(t, p, warm[0]) == "done" })
	before := vmHWM(t, w.cmd.Process.Pid)

	cases := []struct {
		name, path, class, reasonHas string
	}{
		{"H2 host fora da allowlist", "https://example.com/foto.jpg", "permanent", "host fora da allowlist"},
		{"H2b credencial na URL", "https://user:senha@picsum.photos/x.jpg", "permanent", "URL com credencial"},
		{"H3 redirect para host interno", srv.URL + "/to-metadata", "permanent", "host fora da allowlist"},
		{"H4 redirect em laço", srv.URL + "/loop", "permanent", "redirects demais"},
		{"H7 corpo acima do limite", srv.URL + "/endless", "permanent", "imagem acima do limite de bytes"},
		{"H8 PNG pequeno com dimensões enormes", srv.URL + "/huge-dims.png", "permanent", "pixels acima do teto"},
		{"H9 HTML como imagem", srv.URL + "/html", "permanent", "imagem inválida"},
		{"H6 resposta lenta", srv.URL + "/slow", "exhausted", "tentativas esgotadas: timeout"},
	}
	urls := make([]string, 0, len(cases)+1)
	for _, c := range cases {
		urls = append(urls, c.path)
	}
	urls = append(urls, srv.URL+"/ok/1") // a boa do mesmo lote
	batchID, ids, bodies := seed(t, p, urls...)
	publish(t, url, bodies...)

	eventually(t, 90*time.Second, "lote hostil terminado", func() bool { return batchDone(t, p, batchID) })

	for i, c := range cases {
		st, class, reason := failureOf(t, p, ids[i])
		if st != "failed" || class == nil || *class != c.class || reason == nil || !strings.Contains(*reason, c.reasonHas) {
			t.Errorf("%s: status=%s classe=%v motivo=%v; quer failed, %s, %q", c.name, st, deref(class), deref(reason), c.class, c.reasonHas)
		}
	}
	if st := photoStatus(t, p, ids[len(ids)-1]); st != "done" {
		t.Errorf("a foto válida do mesmo lote terminou %s, quer done", st)
	}
	if n := srv.slowHits.Load(); n != 5 { // 1 tentativa + 4 degraus
		t.Errorf("/slow foi buscada %d vezes, quer 5 (escada inteira)", n)
	}
	if !w.alive() {
		t.Fatal("o worker caiu")
	}
	after := vmHWM(t, w.cmd.Process.Pid)
	t.Logf("H8: VmHWM do worker antes=%d kB depois=%d kB (crescimento %d kB)", before, after, after-before)
	// decodificar 6000x6000 RGBA custaria 140.000 kB; DecodeConfig não aloca a imagem
	if after-before > 65536 {
		t.Errorf("a memória do worker cresceu %d kB: a imagem gigante parece ter sido decodificada", after-before)
	}
	// 8 falhas foram à DLQ (a /slow esgotou os degraus); nada ficou preso na fila principal
	eventually(t, 10*time.Second, "DLQ com as 8 mensagens", func() bool { return queueMessages(t, url, topology.PhotoDLQ.Name) == len(cases) })
	if n := queueMessages(t, url, topology.PhotoProcess.Name); n != 0 {
		t.Errorf("photo.process com %d mensagens", n)
	}
}

func deref(s *string) string {
	if s == nil {
		return "<nil>"
	}
	return *s
}

// H5: host da allowlist que resolve para IP interno. Worker SEM o modo de teste (HTTPS, porta 443,
// IP checado no Dial): localhost e o IP literal são recusados sem conectar, como definitivas.
func TestHostilHostQueResolveParaIPInternoENaoConecta(t *testing.T) {
	url := setupBroker(t)
	dbURL, p := migrated(t)
	thumbs := t.TempDir()
	// "=0" sobrepõe o "=1" que o startWorker põe por padrão: aqui valem HTTPS, porta 443 e a checagem do IP
	w := startWorker(t, url, dbURL, thumbs, "localhost,127.0.0.1", "WORKER_TEST_RELAX_FETCH=0")
	batchID, ids, bodies := seed(t, p,
		"https://localhost/foto.jpg",
		"https://127.0.0.1/foto.jpg",
		"https://localhost:8443/foto.jpg",
	)
	publish(t, url, bodies...)
	eventually(t, 30*time.Second, "lote terminado", func() bool { return batchDone(t, p, batchID) })
	want := []string{"endereço IP bloqueado", "endereço IP bloqueado", "porta não permitida"}
	for i, id := range ids {
		st, class, reason := failureOf(t, p, id)
		if st != "failed" || class == nil || *class != "permanent" || reason == nil || *reason != want[i] {
			t.Errorf("%d: status=%s classe=%v motivo=%v; quer failed, permanent, %q", i, st, deref(class), deref(reason), want[i])
		}
	}
	if !w.alive() {
		t.Fatal("o worker caiu")
	}
}

// H10: mensagens malformadas no worker vão à DLQ como definitivas, sem laço de reentrega, e a boa passa.
func TestHostilMensagemMalformadaNoWorkerVaiParaADLQSemLaco(t *testing.T) {
	url := setupBroker(t)
	dbURL, p := migrated(t)
	srv, thumbs := newHostileServer(t), t.TempDir()
	startWorker(t, url, dbURL, thumbs, "127.0.0.1")
	_, ids, bodies := seed(t, p, srv.URL+"/ok/1")

	bad := [][]byte{
		[]byte(`{"v":1,`), // JSON quebrado
		[]byte(`{"v":1,"message_id":"m","batch_id":"nao-uuid","photo_id":"nao-uuid","source_url":"https://x/y"}`),
		[]byte(`{"v":99,"message_id":"m","batch_id":"11111111-1111-4111-8111-111111111111","photo_id":"22222222-2222-4222-8222-222222222222","source_url":"https://x/y"}`),
		[]byte(`{"v":1,"message_id":"m","batch_id":"11111111-1111-4111-8111-111111111111","photo_id":"22222222-2222-4222-8222-222222222222","source_url":"` + strings.Repeat("a", 1<<20) + `"}`), // campo de 1 MiB
		[]byte(`{"v":1,"message_id":"m","batch_id":"11111111-1111-4111-8111-111111111111","photo_id":"33333333-3333-4333-8333-333333333333","source_url":"https://x/y"}`),                        // foto inexistente
	}
	publish(t, url, append(bad, bodies...)...)

	eventually(t, 30*time.Second, "foto válida processada", func() bool { return photoStatus(t, p, ids[0]) == "done" })
	eventually(t, 15*time.Second, "5 mensagens na DLQ", func() bool { return queueMessages(t, url, topology.PhotoDLQ.Name) == len(bad) })
	time.Sleep(2 * time.Second) // sem laço: a contagem não cresce
	if n := queueMessages(t, url, topology.PhotoDLQ.Name); n != len(bad) {
		t.Errorf("DLQ cresceu para %d (laço de reentrega?), quer %d", n, len(bad))
	}
	if n := queueMessages(t, url, topology.PhotoProcess.Name); n != 0 {
		t.Errorf("photo.process com %d mensagens", n)
	}
}

// H1 e variações no ingest: nenhuma entrada hostil grava nada.
func TestHostilIngestRecusaEntradasHostis(t *testing.T) {
	url, dbURL := setupBroker(t), freshDB(t)
	in := startIngest(t, url, dbURL)
	p := pool(t, dbURL)

	many := make([]string, 101)
	for i := range many {
		many[i] = `{"source_url":"https://picsum.photos/id/1/10/10"}`
	}
	cases := map[string]string{
		"host fora da allowlist": `{"photos":[{"source_url":"https://example.com/foto.jpg"}]}`,
		"IP literal interno":     `{"photos":[{"source_url":"https://127.0.0.1/foto.jpg"}]}`,
		"metadados da nuvem":     `{"photos":[{"source_url":"https://169.254.169.254/latest/meta-data/"}]}`,
		"esquema http":           `{"photos":[{"source_url":"http://picsum.photos/id/1/10/10"}]}`,
		"esquema file":           `{"photos":[{"source_url":"file:///etc/passwd"}]}`,
		"credencial na URL":      `{"photos":[{"source_url":"https://user:senha@picsum.photos/x.jpg"}]}`,
		"host parecido (sufixo)": `{"photos":[{"source_url":"https://picsum.photos.evil.com/x.jpg"}]}`,
		"mais de 100 fotos":      `{"photos":[` + strings.Join(many, ",") + `]}`,
		"JSON malformado":        `{"photos":[{"source_url":`,
		"campo de tipo errado":   `{"photos":"não é lista"}`,
		"URL gigante":            `{"photos":[{"source_url":"https://picsum.photos/` + strings.Repeat("a", 5000) + `"}]}`,
	}
	for name, b := range cases {
		if status, _, _ := in.post(t, "", b); status != 400 {
			t.Errorf("%s: status %d, quer 400", name, status)
		}
	}
	// corpo acima de 1 MiB: recusado sem ler tudo (413 ou 400, nunca 202)
	if status, _, _ := in.post(t, "", `{"photos":[{"source_url":"https://picsum.photos/`+strings.Repeat("b", 2<<20)+`"}]}`); status != 413 && status != 400 {
		t.Errorf("corpo de 2 MiB: status %d, quer 413 ou 400", status)
	}
	for _, tbl := range []string{"batches", "photos", "outbox", "events"} {
		if n := count(t, p, "SELECT count(*) FROM "+tbl); n != 0 {
			t.Errorf("%s tem %d linhas depois de só entradas hostis", tbl, n)
		}
	}
	if in.status("/healthz") != 200 {
		t.Error("o ingest caiu")
	}
}
