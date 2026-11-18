package app_test

import (
	"context"
	"errors"
	"log/slog"
	"reflect"
	"testing"
	"time"

	"github.com/MarceloVilela/microservices-go/ms-worker/internal/app"
	"github.com/MarceloVilela/microservices-go/ms-worker/internal/domain"
)

var ttls = []time.Duration{5 * time.Second, 30 * time.Second, 2 * time.Minute, 5 * time.Minute}

const body = `{"v":1,"message_id":"m1","batch_id":"11111111-1111-4111-8111-111111111111","photo_id":"22222222-2222-4222-8222-222222222222","source_url":"https://picsum.photos/id/1/10/10"}`

// rig registra a ordem das chamadas e injeta falhas por etapa.
type rig struct {
	calls []string

	status                  string
	statusSeq               []string // respostas sucessivas de Status (sobrepõe status)
	statusErr               error
	fetchErr, thumbErr      error
	saveErr, finishErr      error
	retryErr, deadLetterErr error
	outcome                 app.Outcome
	cancelOnFetch           context.CancelFunc

	finished []app.Finish
	retried  []int
	dlq      []string // "classe:motivo"
}

func (r *rig) Status(context.Context, string) (string, error) {
	r.calls = append(r.calls, "status")
	if len(r.statusSeq) > 0 {
		st := r.statusSeq[0]
		r.statusSeq = r.statusSeq[1:]
		return st, nil
	}
	if r.status == "" {
		return app.StatusPending, r.statusErr
	}
	return r.status, r.statusErr
}

func (r *rig) Finish(_ context.Context, f app.Finish) (app.Outcome, error) {
	r.calls = append(r.calls, "finish:"+f.Status)
	if r.finishErr == nil {
		r.finished = append(r.finished, f)
	}
	return r.outcome, r.finishErr
}

func (r *rig) Fetch(context.Context, string) ([]byte, error) {
	r.calls = append(r.calls, "fetch")
	if r.cancelOnFetch != nil {
		r.cancelOnFetch()
	}
	return []byte("img"), r.fetchErr
}

func (r *rig) Thumbnail([]byte) ([]byte, error) {
	r.calls = append(r.calls, "thumbnail")
	return []byte("jpg"), r.thumbErr
}

func (r *rig) Save(string, []byte) error { r.calls = append(r.calls, "save"); return r.saveErr }

func (r *rig) Retry(_ context.Context, step int, _ app.Delivery) error {
	r.calls = append(r.calls, "retry")
	r.retried = append(r.retried, step)
	return r.retryErr
}

func (r *rig) DeadLetter(_ context.Context, _ app.Delivery, c domain.Class, reason string) error {
	r.calls = append(r.calls, "dlq")
	r.dlq = append(r.dlq, string(c)+":"+reason)
	return r.deadLetterErr
}

func (r *rig) proc() *app.Processor {
	return &app.Processor{Photos: r, Fetcher: r, Thumbnailer: r, Thumbs: r, Router: r, StepTTLs: ttls, Log: slog.New(slog.DiscardHandler)}
}

func handle(r *rig, attempt int) app.Action {
	return r.proc().Handle(context.Background(), app.Delivery{Body: []byte(body), MessageID: "m1", Attempt: attempt})
}

func wantCalls(t *testing.T, r *rig, want ...string) {
	t.Helper()
	if !reflect.DeepEqual(r.calls, want) {
		t.Errorf("chamadas = %v\nquer      %v", r.calls, want)
	}
}

func TestSucessoSegueAOrdemDaADR0006(t *testing.T) {
	r := &rig{}
	if got := handle(r, 0); got != app.Ack {
		t.Fatalf("ação = %v", got)
	}
	// estado -> baixar -> miniatura -> arquivo -> transação (o ack é do chamador, depois)
	wantCalls(t, r, "status", "fetch", "thumbnail", "save", "finish:done")
}

func TestFotoJaTerminadaSoDaAck(t *testing.T) {
	for _, st := range []string{app.StatusDone, app.StatusFailed} {
		r := &rig{status: st}
		if got := handle(r, 0); got != app.Ack {
			t.Errorf("%s: ação = %v", st, got)
		}
		wantCalls(t, r, "status")
	}
}

func TestErroDoBancoNoEstadoConsomeDegrau(t *testing.T) {
	r := &rig{statusErr: errors.New("conexão caiu")}
	if got := handle(r, 1); got != app.Ack {
		t.Fatalf("ação = %v", got)
	}
	wantCalls(t, r, "status", "retry")
	if !reflect.DeepEqual(r.retried, []int{1}) {
		t.Errorf("degrau = %v", r.retried)
	}
}

func TestInfraEsgotadaVaiParaADLQSemFecharAFoto(t *testing.T) {
	r := &rig{statusErr: errors.New("conexão caiu")}
	if got := handle(r, 4); got != app.Ack {
		t.Fatalf("ação = %v", got)
	}
	// sem Finish (o banco está fora) e sem reler o estado: a foto segue pending
	wantCalls(t, r, "status", "dlq")
	if r.dlq[0] != "infrastructure:tentativas esgotadas: banco indisponível" {
		t.Errorf("dlq = %v", r.dlq)
	}
}

func TestRetryAfterEscolheODegrau(t *testing.T) {
	r := &rig{fetchErr: &domain.Failure{Class: domain.Transient, Reason: "HTTP 429", RetryAfter: 45 * time.Second}}
	if got := handle(r, 0); got != app.Ack {
		t.Fatalf("ação = %v", got)
	}
	if !reflect.DeepEqual(r.retried, []int{2}) { // 45 s => retry.2m
		t.Errorf("degrau = %v", r.retried)
	}
}

func TestDLQReleEstadoEDesisteSeAFotoJaTerminou(t *testing.T) {
	r := &rig{fetchErr: domain.NewPermanent("HTTP 404", nil), statusSeq: []string{app.StatusPending, app.StatusDone}}
	if got := handle(r, 0); got != app.Ack {
		t.Fatalf("ação = %v", got)
	}
	wantCalls(t, r, "status", "fetch", "status") // sem dlq nem finish
}

func TestFotoInexistenteVaiParaADLQ(t *testing.T) {
	r := &rig{statusErr: app.ErrNotFound}
	if got := handle(r, 0); got != app.Ack {
		t.Fatalf("ação = %v", got)
	}
	wantCalls(t, r, "status", "dlq")
}

func TestMensagemInvalidaVaiParaADLQ(t *testing.T) {
	for _, b := range []string{`nao e json`, `{"v":2,"photo_id":"p","batch_id":"b","source_url":"u"}`, `{"v":1}`,
		// ids que não são UUID: sem isto o Postgres falharia sempre e a mensagem seria requeueada para sempre
		`{"v":1,"batch_id":"11111111-1111-4111-8111-111111111111","photo_id":"abc","source_url":"u"}`,
		`{"v":1,"batch_id":"x","photo_id":"22222222-2222-4222-8222-222222222222","source_url":"u"}`} {
		r := &rig{}
		got := r.proc().Handle(context.Background(), app.Delivery{Body: []byte(b)})
		if got != app.Ack {
			t.Errorf("%q: ação = %v", b, got)
		}
		wantCalls(t, r, "dlq")
	}
}

func TestFalhaDefinitivaVaiDireitoParaADLQEContaComoTerminada(t *testing.T) {
	r := &rig{fetchErr: domain.NewPermanent("HTTP 404", errors.New("x"))}
	if got := handle(r, 0); got != app.Ack {
		t.Fatalf("ação = %v", got)
	}
	// DLQ (com confirm) antes da transação; nada de arquivo
	wantCalls(t, r, "status", "fetch", "status", "dlq", "finish:failed")
	if r.dlq[0] != "permanent:HTTP 404" {
		t.Errorf("dlq = %v", r.dlq)
	}
	f := r.finished[0]
	if f.FailClass != "permanent" || f.FailReason != "HTTP 404" {
		t.Errorf("finish = %+v", f)
	}
}

func TestImagemInvalidaVemDaMiniatura(t *testing.T) {
	r := &rig{thumbErr: domain.NewPermanent("imagem inválida", errors.New("x"))}
	handle(r, 0)
	wantCalls(t, r, "status", "fetch", "thumbnail", "status", "dlq", "finish:failed")
}

func TestFalhaTransitoriaVaiParaODegrauEDaAck(t *testing.T) {
	for attempt := range 4 {
		r := &rig{fetchErr: domain.NewTransient("HTTP 503", errors.New("x"))}
		if got := handle(r, attempt); got != app.Ack {
			t.Fatalf("attempt %d: ação = %v", attempt, got)
		}
		wantCalls(t, r, "status", "fetch", "retry") // sem transação: a foto continua pending
		if !reflect.DeepEqual(r.retried, []int{attempt}) {
			t.Errorf("attempt %d: degrau = %v", attempt, r.retried)
		}
	}
}

func TestDegrausEsgotadosVaoParaADLQEFecham(t *testing.T) {
	r := &rig{fetchErr: domain.NewTransient("HTTP 503", errors.New("x"))}
	if got := handle(r, 4); got != app.Ack {
		t.Fatalf("ação = %v", got)
	}
	wantCalls(t, r, "status", "fetch", "status", "dlq", "finish:failed")
	if r.dlq[0] != "exhausted:tentativas esgotadas: HTTP 503" {
		t.Errorf("dlq = %v", r.dlq)
	}
}

func TestFalhaAoRepublicarDevolveAFilaSemFecharAFoto(t *testing.T) {
	r := &rig{fetchErr: domain.NewTransient("timeout", nil), retryErr: errors.New("broker fora")}
	if got := handle(r, 0); got != app.Requeue {
		t.Fatalf("ação = %v", got)
	}
	if len(r.finished) != 0 {
		t.Errorf("não pode fechar a foto: %v", r.finished)
	}
}

func TestFalhaNaDLQNaoFechaAFoto(t *testing.T) {
	r := &rig{fetchErr: domain.NewPermanent("HTTP 404", nil), deadLetterErr: errors.New("broker fora")}
	if got := handle(r, 0); got != app.Requeue {
		t.Fatalf("ação = %v", got)
	}
	wantCalls(t, r, "status", "fetch", "status", "dlq") // sem finish: a reentrega repete tudo
}

func TestErroDoBancoNaTransacaoConsomeDegrau(t *testing.T) {
	r := &rig{finishErr: errors.New("deadlock")}
	if got := handle(r, 0); got != app.Ack {
		t.Fatalf("ação = %v", got)
	}
	// o arquivo já está no lugar (rename atômico): a reentrega o sobrescreve com o mesmo conteúdo
	wantCalls(t, r, "status", "fetch", "thumbnail", "save", "finish:done", "retry")
}

func TestErroDeDiscoConsomeDegrau(t *testing.T) {
	r := &rig{saveErr: errors.New("disco cheio")}
	if got := handle(r, 0); got != app.Ack {
		t.Fatalf("ação = %v", got)
	}
	wantCalls(t, r, "status", "fetch", "thumbnail", "save", "retry")
}

func TestDesligandoNaoGastaDegrau(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	r := &rig{fetchErr: domain.NewTransient("cancelado", context.Canceled), cancelOnFetch: cancel}
	got := r.proc().Handle(ctx, app.Delivery{Body: []byte(body)})
	if got != app.Requeue {
		t.Fatalf("ação = %v", got)
	}
	wantCalls(t, r, "status", "fetch")
}

func TestRedeliveryDuplicadoNaoQuebra(t *testing.T) {
	r := &rig{outcome: app.Outcome{Duplicate: true}}
	if got := handle(r, 0); got != app.Ack {
		t.Fatalf("ação = %v", got)
	}
}

func TestErroSemClasseEhTransitorio(t *testing.T) {
	if c, _ := domain.Classify(errors.New("qualquer")); c != domain.Transient {
		t.Errorf("classe = %s", c)
	}
}

func TestErroDoFetcherSemClasseVaiParaORetry(t *testing.T) {
	r := &rig{fetchErr: errors.New("conexão resetada")}
	if got := handle(r, 0); got != app.Ack {
		t.Fatalf("ação = %v", got)
	}
	wantCalls(t, r, "status", "fetch", "retry")
}
