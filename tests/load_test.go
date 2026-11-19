//go:build integration && load

// Fase 7, bloco D (G2): carga com números reais. Roda com `make load` (fora do `make accept`, demora).
// Processos nativos (a imagem de produção não aceita loopback) contra Postgres e RabbitMQ em containers e
// o servidor de imagens local. Cada medida sai em t.Logf com o prefixo "CARGA"; nenhuma tem meta fixa.
package tests

import (
	"bytes"
	"context"
	"fmt"
	"image"
	"image/color"
	"image/jpeg"
	"math/rand/v2"
	"net/http"
	"net/http/httptest"
	"os"
	"runtime"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// photoLikeJPEG é um JPEG 1920x1280 com ruído (parecido com foto, ~centenas de KB), para o custo de
// decodificação e de redução não ser o de um degradê.
func photoLikeJPEG() []byte {
	const w, h = 1920, 1280
	rng := rand.New(rand.NewPCG(1, 2))
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := range h {
		for x := range w {
			n := rng.IntN(41) - 20
			img.Set(x, y, color.RGBA{clamp(x*255/w + n), clamp(y*255/h + n), clamp(128 + n), 255})
		}
	}
	var b bytes.Buffer
	_ = jpeg.Encode(&b, img, &jpeg.Options{Quality: 80})
	return b.Bytes()
}

func clamp(v int) uint8 { return uint8(min(max(v, 0), 255)) }

// loadServer conta requisições por segundo e serve /ok/ e /flaky/<n> (503 nas 2 primeiras de cada n).
type loadServer struct {
	*httptest.Server
	mu     sync.Mutex
	perSec map[int64]int
	hits   map[string]int
	total  atomic.Int64
}

func newLoadServer(t *testing.T, img []byte) *loadServer {
	t.Helper()
	s := &loadServer{perSec: map[int64]int{}, hits: map[string]int{}}
	s.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		s.total.Add(1)
		s.mu.Lock()
		s.perSec[time.Now().Unix()]++
		s.hits[r.URL.Path]++
		n := s.hits[r.URL.Path]
		s.mu.Unlock()
		if strings.HasPrefix(r.URL.Path, "/flaky/") && n <= 2 {
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		_, _ = w.Write(img)
	}))
	t.Cleanup(s.Close)
	return s
}

// peak devolve o maior número de requisições em 1 s e a mediana dos segundos com tráfego.
func (s *loadServer) peak() (peak, median int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var v []int
	for _, n := range s.perSec {
		v = append(v, n)
	}
	if len(v) == 0 {
		return 0, 0
	}
	slices.Sort(v)
	return v[len(v)-1], v[len(v)/2]
}

func cpuModel() string {
	b, _ := os.ReadFile("/proc/cpuinfo")
	for _, l := range strings.Split(string(b), "\n") {
		if strings.HasPrefix(l, "model name") {
			return strings.TrimSpace(l[strings.Index(l, ":")+1:])
		}
	}
	return "?"
}

func vmRSS(t *testing.T, pid int) int {
	t.Helper()
	b, _ := os.ReadFile(fmt.Sprintf("/proc/%d/status", pid))
	for _, l := range strings.Split(string(b), "\n") {
		var n int
		if _, err := fmt.Sscanf(l, "VmRSS: %d kB", &n); err == nil {
			return n
		}
	}
	return 0
}

// seedBatches grava `batches` lotes de `perBatch` fotos com a mesma URL e devolve ids e mensagens.
func seedBatches(t *testing.T, p *pgxpool.Pool, batches, perBatch int, urlOf func(i int) string) ([]string, [][]byte) {
	t.Helper()
	var ids []string
	var bodies [][]byte
	n := 0
	for range batches {
		urls := make([]string, perBatch)
		for j := range urls {
			urls[j] = urlOf(n)
			n++
		}
		id, _, b := seed(t, p, urls...)
		ids, bodies = append(ids, id), append(bodies, b...)
	}
	return ids, bodies
}

func allDone(t *testing.T, p *pgxpool.Pool, ids []string) bool {
	for _, id := range ids {
		if !batchDone(t, p, id) {
			return false
		}
	}
	return true
}

// runLoad processa os lotes com um worker novo e devolve duração, pico de memória e RSS final.
//
// Cada cenário é um subteste: o t.Cleanup que encerra o worker roda ao fim dele. Sem isso o worker do
// cenário anterior seguiria vivo no broker compartilhado, consumindo mensagens de outro banco.
func runLoad(t *testing.T, name string, concurrency, batches, perBatch int, extra ...string) {
	t.Helper()
	t.Run(name, func(t *testing.T) { runLoadScenario(t, name, concurrency, batches, perBatch, extra...) })
}

func runLoadScenario(t *testing.T, name string, concurrency, batches, perBatch int, extra ...string) {
	url := setupBroker(t)
	dbURL, p := migrated(t)
	srv := newLoadServer(t, photoLikeJPEG())
	thumbs := t.TempDir()
	w := startWorker(t, url, dbURL, thumbs, "127.0.0.1", append([]string{
		fmt.Sprintf("WORKER_CONCURRENCY=%d", concurrency), fmt.Sprintf("WORKER_PREFETCH=%d", concurrency),
		"FETCH_TIMEOUT=20s", "LOG_LEVEL=warn"}, extra...)...)
	w.waitReady(t)
	ids, bodies := seedBatches(t, p, batches, perBatch, func(i int) string { return fmt.Sprintf("%s/ok/%d", srv.URL, i) })

	start := time.Now()
	publish(t, url, bodies...)
	eventually(t, 10*time.Minute, name+": lotes terminados", func() bool { return allDone(t, p, ids) })
	secs := time.Since(start).Seconds()
	total := batches * perBatch
	t.Logf("CARGA %-34s %4d fotos em %6.1f s = %6.1f fotos/s | worker VmHWM %6d kB, VmRSS %6d kB | %d requisições ao servidor",
		name, total, secs, float64(total)/secs, vmHWM(t, w.cmd.Process.Pid), vmRSS(t, w.cmd.Process.Pid), srv.total.Load())
	if n := count(t, p, `SELECT count(*) FROM photos WHERE status = 'done'`); n != total {
		t.Errorf("%s: %d fotos done, esperado %d", name, n, total)
	}
}

func TestCarga(t *testing.T) {
	t.Logf("CARGA máquina: %s, %d CPUs lógicas, %s", cpuModel(), runtime.NumCPU(), runtime.Version())
	t.Logf("CARGA imagem de teste: %d bytes (JPEG 1920x1280 com ruído, q80)", len(photoLikeJPEG()))

	// 1. vazão e memória: FIFO (concorrência 1, ADR 0013) × concorrência 4
	t.Run("vazao", func(t *testing.T) {
		runLoad(t, "500 fotos, 5 lotes de 100, conc=1", 1, 5, 100)
		runLoad(t, "500 fotos, 5 lotes de 100, conc=4", 4, 5, 100)
	})

	// 3. contenção na linha do lote (ADR 0014): 1 lote de 500 × 5 de 100, ambos com conc=4
	t.Run("contencao_do_lote", func(t *testing.T) {
		runLoad(t, "1 lote de 500, conc=4", 4, 1, 500)
		runLoad(t, "5 lotes de 100, conc=4 (repetição)", 4, 5, 100)
	})

	// 4. relay: 2000 linhas do outbox publicadas sem consumidor, por tamanho de lote
	t.Run("relay", func(t *testing.T) {
		for _, size := range []string{"100", "500"} {
			url, dbURL := setupBroker(t), freshDB(t)
			p := pool(t, dbURL)
			startIngest(t, url, dbURL, "RELAY_BATCH_SIZE="+size, "RELAY_POLL_INTERVAL=500ms", "LOG_LEVEL=warn")
			if _, err := p.Exec(context.Background(), `INSERT INTO outbox (routing_key, payload)
				SELECT 'process', jsonb_build_object('v', 1, 'message_id', g::text) FROM generate_series(1, 2000) g`); err != nil {
				t.Fatal(err)
			}
			start := time.Now()
			eventually(t, 5*time.Minute, "outbox publicado", func() bool {
				return count(t, p, `SELECT count(*) FROM outbox WHERE published_at IS NULL`) == 0
			})
			secs := time.Since(start).Seconds()
			t.Logf("CARGA relay RELAY_BATCH_SIZE=%-3s 2000 linhas em %5.1f s = %7.1f msg/s", size, secs, 2000/secs)
		}
	})

	// 5. escada (ADR 0005): 200 fotos que falham 2 vezes (503) e depois passam; os degraus reais (5 s e 30 s)
	//    agrupam as retentativas em ondas. Pico de req/s nas ondas × tráfego normal.
	t.Run("escada_herd", func(t *testing.T) {
		url := setupBroker(t)
		dbURL, p := migrated(t)
		srv := newLoadServer(t, photoLikeJPEG())
		w := startWorker(t, url, dbURL, t.TempDir(), "127.0.0.1", "WORKER_CONCURRENCY=4", "WORKER_PREFETCH=4", "FETCH_TIMEOUT=20s", "LOG_LEVEL=warn")
		w.waitReady(t)
		ids, bodies := seedBatches(t, p, 2, 100, func(i int) string { return fmt.Sprintf("%s/flaky/%d", srv.URL, i) })
		start := time.Now()
		publish(t, url, bodies...)
		eventually(t, 5*time.Minute, "lotes terminados", func() bool { return allDone(t, p, ids) })
		peak, median := srv.peak()
		failed := count(t, p, `SELECT count(*) FROM photos WHERE status = 'failed'`)
		t.Logf("CARGA escada: 200 fotos com 2 falhas 503 cada, terminou em %.1f s; %d requisições ao servidor (esperado 600); "+
			"pico %d req/s, mediana dos segundos com tráfego %d req/s; %d fotos failed",
			time.Since(start).Seconds(), srv.total.Load(), peak, median, failed)
		if failed != 0 {
			t.Errorf("%d fotos terminaram failed; com 2 falhas e 4 degraus todas deveriam passar", failed)
		}
		if srv.total.Load() != 600 {
			t.Errorf("requisições = %d, esperado 600 (200 fotos x 3 tentativas)", srv.total.Load())
		}
	})
}
