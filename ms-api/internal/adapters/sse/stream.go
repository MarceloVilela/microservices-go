// Package sse serve GET /events (ADR 0016): o log de eventos do Postgres como Server-Sent Events.
//
// O `id` do evento é o `seq` da tabela `events`; o cliente retoma com `Last-Event-ID`. O NOTIFY do
// Postgres só acorda os streams: a verdade é sempre a leitura por `seq > último`, com um tick de
// segurança para o caso de um NOTIFY se perder (reconexão do LISTEN, por exemplo).
package sse

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strconv"
	"sync"
	"sync/atomic"
	"time"

	"github.com/MarceloVilela/microservices-go/ms-api/internal/app"
	"github.com/MarceloVilela/microservices-go/platform/httpx"
)

// Route é o padrão em que o Handler é montado.
const Route = "GET /events"

// Source é o log de eventos (interface no consumidor).
type Source interface {
	// Bounds devolve o menor e o maior seq retidos; (0, 0) com o log vazio.
	Bounds(ctx context.Context) (minSeq, maxSeq int64, err error)
	// After devolve até limit eventos com seq > after, em ordem crescente.
	After(ctx context.Context, after int64, limit int) ([]app.Event, error)
}

// Waker avisa que pode haver evento novo. O aviso é só uma dica: perder um não perde evento.
type Waker interface {
	Subscribe() (wake <-chan struct{}, cancel func())
}

// Handler é o endpoint SSE. A autenticação e o CORS ficam nos middlewares de quem o monta.
type Handler struct {
	Source Source
	Waker  Waker
	// Shutdown termina todos os streams (SIGTERM do serviço): eles enviam `event: shutdown` e fecham.
	Shutdown     context.Context
	Log          *slog.Logger
	Heartbeat    time.Duration // comentário `: ping` em silêncio (~15 s, contra timeout de proxy)
	Poll         time.Duration // tick de segurança (2 s)
	WriteTimeout time.Duration // prazo de cada escrita; estourou ⇒ cliente lento, desconecta
	GapWait      time.Duration // quanto esperar por um seq que falta antes de aceitá-lo como rollback
	MaxPerRead   int           // eventos por consulta (200)

	clients atomic.Int64
}

// Clients é o número de streams abertos (para o /statusz).
func (h *Handler) Clients() int64 { return h.clients.Load() }

// ServeHTTP implementa GET /events.
func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	lastID, hasLast, err := parseLastEventID(r.Header.Get("Last-Event-ID"))
	if err != nil {
		httpx.Problem(w, http.StatusBadRequest, "invalid_last_event_id", "Last-Event-ID deve ser um inteiro não negativo")
		return
	}
	minSeq, maxSeq, err := h.Source.Bounds(ctx)
	if err != nil {
		h.Log.Error("sse: ler limites do log", "erro", err)
		httpx.Problem(w, http.StatusInternalServerError, "internal_error", "erro interno")
		return
	}

	s := &session{h: h, w: w, rc: http.NewResponseController(w)}
	switch {
	case !hasLast:
		s.last = maxSeq // cliente novo só vê o futuro; o estado inicial vem pelo REST
	case lastID > maxSeq:
		s.last, s.reset = maxSeq, true // o cliente viu seq que este log não tem (banco recriado)
	case minSeq > 0 && lastID < minSeq-1:
		s.last, s.reset = minSeq-1, true // linhas entre lastID e minSeq foram podadas
	default:
		s.last = lastID
	}

	h.clients.Add(1)
	defer h.clients.Add(-1)
	wake, cancel := h.Waker.Subscribe()
	defer cancel()

	hdr := w.Header()
	hdr.Set("Content-Type", "text/event-stream")
	hdr.Set("Cache-Control", "no-cache")
	hdr.Set("X-Accel-Buffering", "no") // proxies nginx não devem acumular o stream
	w.WriteHeader(http.StatusOK)
	if s.reset {
		if s.send("event: reset\ndata: {\"reason\":\"retention\"}\n\n") != nil {
			return
		}
	} else if s.send(": ok\n\n") != nil {
		return
	}

	poll, beat := time.NewTicker(h.Poll), time.NewTicker(h.Heartbeat)
	defer poll.Stop()
	defer beat.Stop()
	for {
		if err := s.drain(ctx); err != nil {
			if ctx.Err() == nil {
				h.Log.Warn("sse: encerrando o stream", "erro", err, "last_seq", s.last)
			}
			return
		}
		select {
		case <-ctx.Done():
			return
		case <-h.Shutdown.Done():
			_ = s.send("event: shutdown\ndata: {}\n\n")
			return
		case <-wake:
		case <-poll.C:
		case <-beat.C:
			if s.send(": ping\n\n") != nil {
				return
			}
		}
	}
}

// parseLastEventID aceita ausente (hasLast=false) ou inteiro >= 0.
func parseLastEventID(s string) (id int64, hasLast bool, err error) {
	if s == "" {
		return 0, false, nil
	}
	id, err = strconv.ParseInt(s, 10, 64)
	if err != nil || id < 0 {
		return 0, false, errors.New("Last-Event-ID inválido")
	}
	return id, true, nil
}

// session é o estado de um stream: o último seq entregue e há quanto tempo um buraco de seq espera.
type session struct {
	h        *Handler
	w        http.ResponseWriter
	rc       *http.ResponseController
	last     int64
	reset    bool
	gapSince time.Time
}

// send escreve com prazo: um cliente que não lê estoura o prazo e é desconectado (retoma pelo seq).
func (s *session) send(frame string) error {
	if err := s.rc.SetWriteDeadline(time.Now().Add(s.h.WriteTimeout)); err != nil && !errors.Is(err, http.ErrNotSupported) {
		return err
	}
	if _, err := fmt.Fprint(s.w, frame); err != nil {
		return err
	}
	return s.rc.Flush()
}

// drain entrega tudo o que já está no log depois de s.last.
func (s *session) drain(ctx context.Context) error {
	for {
		rows, err := s.h.Source.After(ctx, s.last, s.h.MaxPerRead)
		if err != nil {
			return err
		}
		ready := s.contiguous(rows)
		for _, ev := range ready {
			data, err := json.Marshal(ev)
			if err != nil {
				return err
			}
			if err := s.send(fmt.Sprintf("id: %d\nevent: %s\ndata: %s\n\n", ev.Seq, ev.Type, data)); err != nil {
				return err
			}
			s.last = ev.Seq
		}
		if len(ready) < s.h.MaxPerRead {
			return nil // acabou o log, ou parou num buraco que ainda pode preencher
		}
	}
}

// contiguous devolve o prefixo de rows que pode sair agora. bigserial não garante que as
// transações commitem na ordem do seq: se o 6 apareceu e o 5 não, o 5 pode ainda estar em voo.
// Entregar o 6 e seguir perderia o 5 para sempre; por isso um buraco segura o que vem depois por
// GapWait (a partir da 1ª vez em que foi visto). Passado o prazo, é rollback e o buraco é aceito.
func (s *session) contiguous(rows []app.Event) []app.Event {
	var out []app.Event
	next := s.last + 1
	for _, ev := range rows {
		if ev.Seq != next {
			if s.gapSince.IsZero() {
				s.gapSince = time.Now()
			}
			if time.Since(s.gapSince) < s.h.GapWait {
				return out
			}
		}
		out = append(out, ev)
		next = ev.Seq + 1
	}
	s.gapSince = time.Time{}
	return out
}

// Broadcaster acorda os streams. Notify nunca bloqueia: cada assinante tem um canal de 1 posição,
// e um aviso já pendente basta (o stream lê tudo o que há por seq).
type Broadcaster struct {
	mu   sync.Mutex
	subs map[chan struct{}]struct{}
}

// Subscribe registra um assinante; chame cancel ao terminar.
func (b *Broadcaster) Subscribe() (<-chan struct{}, func()) {
	ch := make(chan struct{}, 1)
	b.mu.Lock()
	if b.subs == nil {
		b.subs = map[chan struct{}]struct{}{}
	}
	b.subs[ch] = struct{}{}
	b.mu.Unlock()
	return ch, func() {
		b.mu.Lock()
		delete(b.subs, ch)
		b.mu.Unlock()
	}
}

// Notify acorda todos os assinantes.
func (b *Broadcaster) Notify() {
	b.mu.Lock()
	defer b.mu.Unlock()
	for ch := range b.subs {
		select {
		case ch <- struct{}{}:
		default:
		}
	}
}
