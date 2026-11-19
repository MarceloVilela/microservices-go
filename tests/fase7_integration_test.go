//go:build integration

// Testes da Fase 7: /metrics dos quatro serviços e foto parada na DLQ por infraestrutura esgotada.
package tests

import (
	"io"
	"net/http"
	"os"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"
)

// scrape lê /metrics (sem Bearer: como /healthz).
func scrape(t *testing.T, base string) string {
	t.Helper()
	resp, err := http.Get(base + "/metrics")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != 200 {
		t.Fatalf("GET %s/metrics: %d", base, resp.StatusCode)
	}
	return string(b)
}

// metric devolve o valor da série (nome com rótulos exatamente como no texto), ou -1 se ausente.
func metric(out, series string) float64 {
	m := regexp.MustCompile(`(?m)^` + regexp.QuoteMeta(series) + ` ([0-9.e+-]+)$`).FindStringSubmatch(out)
	if m == nil {
		return -1
	}
	v, _ := strconv.ParseFloat(m[1], 64)
	return v
}

func TestMetricsDosQuatroServicos(t *testing.T) {
	url := setupBroker(t)
	dbURL, p := migrated(t)
	thumbs := t.TempDir()
	srv := newImageServer(t)

	in := startIngest(t, url, dbURL)
	a := startAPI(t, url, dbURL, thumbs)
	w := startWorker(t, url, dbURL, thumbs, "127.0.0.1")
	n := startNotification(t, url, dbURL)

	// uma linha em quarentena já existente aparece como gauge
	if _, err := p.Exec(t.Context(), `INSERT INTO outbox (routing_key, payload, parked_at, attempts) VALUES ('sem-fila', '{"message_id":"ruim"}', now(), 3)`); err != nil {
		t.Fatal(err)
	}

	batchID, ids, bodies := seed(t, p, srv.URL+"/ok/1", srv.URL+"/missing")
	publish(t, url, bodies...)
	eventually(t, 30*time.Second, "lote terminado", func() bool { return batchDone(t, p, batchID) })
	eventually(t, 20*time.Second, "notificação do lote", func() bool {
		return metric(scrape(t, n.base), `notifications_total{outcome="notified"}`) == 1
	})

	// worker
	wm := scrape(t, w.base)
	if v := metric(wm, `photos_processed_total{outcome="done"}`); v != 1 {
		t.Errorf("worker done = %v, quer 1", v)
	}
	if v := metric(wm, `photos_processed_total{outcome="dlq"}`); v != 1 {
		t.Errorf("worker dlq = %v, quer 1 (a 404 é definitiva)", v)
	}
	if metric(wm, `photo_process_seconds_count{outcome="done"}`) != 1 || !strings.Contains(wm, `photo_process_seconds_bucket{outcome="done",le="`) {
		t.Errorf("histograma de latência ausente:\n%s", wm)
	}
	// a 404 foi à DLQ (a mensagem ficou lá); as demais filas estão vazias
	eventually(t, 10*time.Second, "queue_messages da DLQ", func() bool {
		return metric(scrape(t, w.base), `queue_messages{queue="photo.process.dlq"}`) == 1
	})
	if v := metric(scrape(t, w.base), `queue_messages{queue="photo.process"}`); v != 0 {
		t.Errorf("queue_messages photo.process = %v, quer 0", v)
	}

	// ingest
	if v := metric(scrape(t, in.base), `outbox_quarantined`); v != 1 {
		t.Errorf("outbox_quarantined = %v, quer 1", v)
	}
	if v := metric(scrape(t, in.base), `outbox_pending`); v < 0 {
		t.Error("outbox_pending ausente")
	}
	if v := metric(scrape(t, in.base), `outbox_oldest_pending_age_seconds`); v < 0 {
		t.Error("outbox_oldest_pending_age_seconds ausente")
	}

	// api (sem token) e cardinalidade
	am := scrape(t, a.base)
	if v := metric(am, `photos_by_status{status="done"}`); v != 1 {
		t.Errorf("photos_by_status done = %v, quer 1", v)
	}
	if v := metric(am, `photos_by_status{status="failed"}`); v != 1 {
		t.Errorf("photos_by_status failed = %v, quer 1", v)
	}
	if metric(am, `sse_clients`) != 0 || metric(am, `sse_last_seq`) < 1 {
		t.Errorf("gauges do SSE: %s", am)
	}
	a.do(t, "GET", "/photos/"+ids[0]+"/thumb")
	am = scrape(t, a.base)
	if !strings.Contains(am, `route="GET /photos/{id}/thumb"`) {
		t.Errorf("rota por padrão ausente:\n%s", am)
	}
	for name, out := range map[string]string{"api": am, "worker": scrape(t, w.base), "ingest": scrape(t, in.base), "notification": scrape(t, n.base)} {
		for _, id := range append(ids, batchID) {
			if strings.Contains(out, id) {
				t.Errorf("%s: id %s vazou para um rótulo (cardinalidade)", name, id)
			}
		}
	}
}

// F7-5 a F7-7: com o disco do worker fora, a foto esgota os degraus, vai à DLQ e segue pending, mas
// o ms-api a lista em GET /dlq e aceita o retry; o lote conta a foto uma única vez.
func TestFotoParadaPorInfraAparecenaDLQERetry(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root ignora permissões de diretório")
	}
	url := setupBroker(t)
	fastRetry(t, 300)
	dbURL, p := migrated(t)
	srv, thumbs := newImageServer(t), t.TempDir()
	if err := os.Chmod(thumbs, 0o555); err != nil { // sem escrita: Save falha ⇒ infraestrutura
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(thumbs, 0o755) })

	a := startAPI(t, url, dbURL, thumbs)
	startIngest(t, url, dbURL) // o relay publica o outbox do retry
	startWorker(t, url, dbURL, thumbs, "127.0.0.1")

	batchID, ids, bodies := seed(t, p, srv.URL+"/ok/1")
	publish(t, url, bodies...)

	var dlq struct {
		Items []map[string]any `json:"items"`
	}
	eventually(t, 60*time.Second, "foto parada na DLQ", func() bool {
		a.getJSON(t, "/dlq", &dlq)
		return len(dlq.Items) == 1
	})
	it := dlq.Items[0]
	if it["id"] != ids[0] || it["status"] != "pending" || it["failure_class"] != "infrastructure" || it["finished_at"] == nil {
		t.Fatalf("item da DLQ = %v", it)
	}
	if st := photoStatus(t, p, ids[0]); st != "pending" {
		t.Fatalf("status = %s, a foto deve seguir pending", st)
	}
	if got := count(t, p, `SELECT completed_count FROM batches WHERE id=$1`, batchID); got != 0 {
		t.Fatalf("completed_count = %d, quer 0 (infra esgotada não conta o lote)", got)
	}
	if v := metric(scrape(t, a.base), `photos_stalled`); v != 1 {
		t.Errorf("photos_stalled = %v, quer 1", v)
	}

	// disco volta; o retry reabre a foto parada
	if err := os.Chmod(thumbs, 0o755); err != nil {
		t.Fatal(err)
	}
	if resp, _ := a.do(t, "POST", "/dlq/"+ids[0]+"/retry"); resp.StatusCode != 202 {
		t.Fatalf("retry da foto parada: %d, quer 202", resp.StatusCode)
	}
	eventually(t, 30*time.Second, "foto done", func() bool { return photoStatus(t, p, ids[0]) == "done" })
	eventually(t, 10*time.Second, "lote fechado", func() bool { return batchDone(t, p, batchID) })
	if got := count(t, p, `SELECT completed_count FROM batches WHERE id=$1`, batchID); got != 1 {
		t.Errorf("completed_count = %d, quer 1 (a foto conta uma vez)", got)
	}
	if n := len(completedMessages(t, p, batchID)); n != 1 {
		t.Errorf("batch.completed no outbox = %d, quer 1", n)
	}
	a.getJSON(t, "/dlq", &dlq)
	if len(dlq.Items) != 0 {
		t.Errorf("a DLQ deveria esvaziar: %v", dlq.Items)
	}
	if resp, _ := a.do(t, "POST", "/dlq/"+ids[0]+"/retry"); resp.StatusCode != 409 {
		t.Errorf("retry de foto done: %d, quer 409", resp.StatusCode)
	}
}
