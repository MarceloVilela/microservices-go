package sse_test

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/MarceloVilela/microservices-go/ms-api/internal/adapters/sse"
	"github.com/MarceloVilela/microservices-go/ms-api/internal/app"
)

// fakeSource é um log de eventos em memória.
type fakeSource struct {
	mu     sync.Mutex
	events []app.Event
	err    error
}

func (f *fakeSource) add(seqs ...int64) {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, s := range seqs {
		f.events = append(f.events, app.Event{Seq: s, Type: "photo.done", Payload: json.RawMessage(`{"status":"done"}`)})
	}
}

func (f *fakeSource) Bounds(context.Context) (int64, int64, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.err != nil {
		return 0, 0, f.err
	}
	if len(f.events) == 0 {
		return 0, 0, nil
	}
	return f.events[0].Seq, f.events[len(f.events)-1].Seq, nil
}

func (f *fakeSource) After(_ context.Context, seq int64, limit int) ([]app.Event, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []app.Event
	for _, e := range f.events {
		if e.Seq > seq && len(out) < limit {
			out = append(out, e)
		}
	}
	return out, f.err
}

type rig struct {
	srv      *httptest.Server
	src      *fakeSource
	bc       *sse.Broadcaster
	h        *sse.Handler
	shutdown context.CancelFunc
}

func newRig(t *testing.T, mutate func(*sse.Handler)) *rig {
	t.Helper()
	src, bc := &fakeSource{}, &sse.Broadcaster{}
	sctx, cancel := context.WithCancel(context.Background())
	h := &sse.Handler{
		Source: src, Waker: bc, Shutdown: sctx, Log: slog.New(slog.DiscardHandler),
		Heartbeat: time.Hour, Poll: time.Hour, WriteTimeout: 2 * time.Second, GapWait: 150 * time.Millisecond, MaxPerRead: 200,
	}
	if mutate != nil {
		mutate(h)
	}
	srv := httptest.NewServer(h)
	t.Cleanup(func() { cancel(); srv.Close() })
	return &rig{srv: srv, src: src, bc: bc, h: h, shutdown: cancel}
}

type stream struct {
	resp  *http.Response
	lines chan string
}

func (r *rig) open(t *testing.T, lastID string) *stream {
	t.Helper()
	req, _ := http.NewRequest(http.MethodGet, r.srv.URL, nil)
	if lastID != "" {
		req.Header.Set("Last-Event-ID", lastID)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = resp.Body.Close() })
	s := &stream{resp: resp, lines: make(chan string, 256)}
	go func() {
		sc := bufio.NewScanner(resp.Body)
		skip := 0 // o ": ok" inicial (que só abre o stream) e a linha em branco dele
		for sc.Scan() {
			if l := sc.Text(); skip == 0 && l == ": ok" {
				skip = 1
			} else if skip == 1 {
				skip = 2
			} else {
				s.lines <- l
			}
		}
		close(s.lines)
	}()
	return s
}

// next devolve a próxima linha (pode ser ""), "<timeout>" se nada veio, ou "<EOF>" se o servidor fechou.
func (s *stream) next(d time.Duration) string {
	select {
	case l, ok := <-s.lines:
		if !ok {
			return "<EOF>"
		}
		return l
	case <-time.After(d):
		return "<timeout>"
	}
}

// ids devolve os `id:` dos próximos n eventos (pula as demais linhas).
func (s *stream) ids(t *testing.T, n int) []string {
	t.Helper()
	var out []string
	deadline := time.After(3 * time.Second)
	for len(out) < n {
		select {
		case l, ok := <-s.lines:
			if !ok {
				t.Fatalf("stream fechou com ids=%v", out)
			}
			if id, found := strings.CutPrefix(l, "id: "); found {
				out = append(out, id)
			}
		case <-deadline:
			t.Fatalf("timeout esperando %d eventos; veio %v", n, out)
		}
	}
	return out
}

func TestClienteNovoSoVeOFuturo(t *testing.T) {
	r := newRig(t, nil)
	r.src.add(1, 2, 3) // histórico: não deve ser reenviado
	s := r.open(t, "")
	if l := s.next(200 * time.Millisecond); l != "<timeout>" {
		t.Fatalf("cliente novo recebeu %q do histórico", l)
	}
	r.src.add(4)
	r.bc.Notify()
	if got := s.ids(t, 1); got[0] != "4" {
		t.Errorf("ids = %v, quer [4]", got)
	}
}

func TestFormatoDoEvento(t *testing.T) {
	r := newRig(t, nil)
	s := r.open(t, "0")
	r.src.add(1)
	r.bc.Notify()
	want := []string{"id: 1", "event: photo.done"}
	for _, w := range want {
		if l := s.next(2 * time.Second); l != w {
			t.Fatalf("linha = %q, quer %q", l, w)
		}
	}
	data := s.next(2 * time.Second)
	payload, ok := strings.CutPrefix(data, "data: ")
	if !ok || !strings.Contains(payload, `"seq":1`) || !strings.Contains(payload, `"payload":{"status":"done"}`) {
		t.Errorf("data = %q", data)
	}
	if l := s.next(time.Second); l != "" { // linha em branco que fecha o evento
		t.Errorf("faltou a linha em branco, veio %q", l)
	}
	if ct := s.resp.Header.Get("Content-Type"); ct != "text/event-stream" {
		t.Errorf("Content-Type = %q", ct)
	}
}

func TestRetomadaPorLastEventIDSemPerdaNemRepeticao(t *testing.T) {
	r := newRig(t, nil)
	r.src.add(1, 2, 3, 4, 5)
	s := r.open(t, "2")
	if got := s.ids(t, 3); strings.Join(got, ",") != "3,4,5" {
		t.Errorf("ids = %v, quer 3,4,5", got)
	}
}

func TestLeituraEmLotesPequenos(t *testing.T) {
	r := newRig(t, func(h *sse.Handler) { h.MaxPerRead = 2 })
	r.src.add(1, 2, 3, 4, 5, 6, 7)
	if got := r.open(t, "0").ids(t, 7); strings.Join(got, ",") != "1,2,3,4,5,6,7" {
		t.Errorf("ids = %v", got)
	}
}

func TestForaDaJanelaDeRetencaoPedeReset(t *testing.T) {
	r := newRig(t, nil)
	r.src.add(50, 51) // as linhas até 49 foram podadas
	s := r.open(t, "10")
	if l := s.next(2 * time.Second); l != "event: reset" {
		t.Fatalf("1ª linha = %q, quer event: reset", l)
	}
	if l := s.next(time.Second); l != `data: {"reason":"retention"}` {
		t.Errorf("data = %q", l)
	}
	s.next(time.Second) // linha em branco
	if got := s.ids(t, 2); strings.Join(got, ",") != "50,51" {
		t.Errorf("depois do reset: ids = %v, quer 50,51", got)
	}
}

func TestLastEventIDNoLimiteDaJanelaNaoPedeReset(t *testing.T) {
	r := newRig(t, nil)
	r.src.add(50, 51)
	s := r.open(t, "49")                             // 49 é o último podado: nada se perdeu
	if l := s.next(2 * time.Second); l != "id: 50" { // o 1º frame é o evento 50, não um reset
		t.Fatalf("1ª linha = %q, quer id: 50", l)
	}
	if got := s.ids(t, 1); got[0] != "51" {
		t.Errorf("id = %v, quer 51", got)
	}
}

func TestLastEventIDDeUmBancoMaisNovoPedeReset(t *testing.T) {
	r := newRig(t, nil)
	r.src.add(1, 2)
	s := r.open(t, "999") // o cliente viu seq que este banco nunca teve
	if l := s.next(2 * time.Second); l != "event: reset" {
		t.Fatalf("1ª linha = %q, quer event: reset", l)
	}
}

func TestLastEventIDInvalido(t *testing.T) {
	r := newRig(t, nil)
	for _, bad := range []string{"abc", "-1", "1.5", "0x10"} {
		req, _ := http.NewRequest(http.MethodGet, r.srv.URL, nil)
		req.Header.Set("Last-Event-ID", bad)
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		var p map[string]any
		_ = json.NewDecoder(resp.Body).Decode(&p)
		_ = resp.Body.Close()
		if resp.StatusCode != 400 || p["code"] != "invalid_last_event_id" {
			t.Errorf("Last-Event-ID %q: %d %v", bad, resp.StatusCode, p)
		}
	}
}

func TestHeartbeatEmSilencio(t *testing.T) {
	r := newRig(t, func(h *sse.Handler) { h.Heartbeat = 50 * time.Millisecond })
	s := r.open(t, "")
	for range 3 {
		if l := s.next(2 * time.Second); l != ": ping" {
			t.Fatalf("linha = %q, quer ': ping'", l)
		}
		s.next(time.Second) // linha em branco
	}
}

func TestPollingDeSegurancaSemNotify(t *testing.T) {
	r := newRig(t, func(h *sse.Handler) { h.Poll = 30 * time.Millisecond })
	s := r.open(t, "0")
	r.src.add(1) // sem Notify: o NOTIFY pode se perder; o tick lê por seq mesmo assim
	if got := s.ids(t, 1); got[0] != "1" {
		t.Errorf("ids = %v", got)
	}
}

func TestBuracoDeSeqEsperaAntesDePularParaNaoPerderTransacaoLenta(t *testing.T) {
	r := newRig(t, func(h *sse.Handler) { h.Poll = 20 * time.Millisecond; h.GapWait = 400 * time.Millisecond })
	r.src.add(1)
	s := r.open(t, "0")
	s.ids(t, 1)
	r.src.add(3) // o seq 2 ainda não commitou
	if l := s.next(150 * time.Millisecond); strings.HasPrefix(l, "id:") {
		t.Fatalf("emitiu %q por cima do buraco antes da espera", l)
	}
	// o 2 commita (fora de ordem): sai antes do 3, sem perda
	r.src.mu.Lock()
	r.src.events = []app.Event{r.src.events[0], {Seq: 2, Type: "photo.done", Payload: json.RawMessage(`{}`)}, r.src.events[1]}
	r.src.mu.Unlock()
	if got := s.ids(t, 2); strings.Join(got, ",") != "2,3" {
		t.Errorf("ids = %v, quer 2,3", got)
	}
}

func TestBuracoPermanentePassaDepoisDaEspera(t *testing.T) {
	r := newRig(t, func(h *sse.Handler) { h.Poll = 20 * time.Millisecond; h.GapWait = 100 * time.Millisecond })
	r.src.add(1, 3) // o 2 foi um rollback: nunca existirá
	s := r.open(t, "0")
	if got := s.ids(t, 2); strings.Join(got, ",") != "1,3" {
		t.Errorf("ids = %v, quer 1,3", got)
	}
}

func TestShutdownEnviaEventoEFecha(t *testing.T) {
	r := newRig(t, nil)
	s := r.open(t, "")
	r.shutdown()
	var seen []string
	for {
		l := s.next(2 * time.Second)
		if l == "<EOF>" || l == "<timeout>" {
			break
		}
		seen = append(seen, l)
	}
	if len(seen) == 0 || seen[0] != "event: shutdown" {
		t.Errorf("linhas = %v, quer começar com event: shutdown e fechar", seen)
	}
}

func TestContadorDeClientes(t *testing.T) {
	r := newRig(t, nil)
	s := r.open(t, "")
	deadline := time.Now().Add(2 * time.Second)
	for r.h.Clients() != 1 && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if r.h.Clients() != 1 {
		t.Fatalf("Clients = %d, quer 1", r.h.Clients())
	}
	_ = s.resp.Body.Close()
	for r.h.Clients() != 0 && time.Now().Before(deadline.Add(2*time.Second)) {
		time.Sleep(10 * time.Millisecond)
	}
	if r.h.Clients() != 0 {
		t.Errorf("Clients = %d depois de fechar, quer 0", r.h.Clients())
	}
}

func TestErroDoBancoFechaOStreamSemVazarDetalhe(t *testing.T) {
	r := newRig(t, nil)
	s := r.open(t, "")
	r.src.mu.Lock()
	r.src.err = errors.New("senha: hunter2")
	r.src.mu.Unlock()
	r.bc.Notify()
	for {
		l := s.next(2 * time.Second)
		if strings.Contains(l, "hunter2") {
			t.Fatal("vazou detalhe do erro")
		}
		if l == "<EOF>" {
			return
		}
		if l == "<timeout>" {
			t.Fatal("o stream deveria fechar quando o banco falha (o cliente reconecta pelo seq)")
		}
	}
}

func TestBroadcasterAcordaTodosENaoBloqueia(t *testing.T) {
	var bc sse.Broadcaster
	a, ua := bc.Subscribe()
	b, ub := bc.Subscribe()
	defer ub()
	for range 1000 { // ninguém lê: Notify não pode travar
		bc.Notify()
	}
	for _, c := range []<-chan struct{}{a, b} {
		select {
		case <-c:
		default:
			t.Error("assinante não foi acordado")
		}
	}
	ua()
	bc.Notify() // assinante removido não recebe (e não entra em pânico)
	select {
	case <-a:
		t.Error("assinante cancelado foi acordado")
	default:
	}
}
