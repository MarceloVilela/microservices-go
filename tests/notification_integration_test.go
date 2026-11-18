//go:build integration

// Testes de integração do ms-notification: o binário real contra Postgres 18 e RabbitMQ 4 em
// containers, com um servidor SMTP falso em processo (o aceite usa o Mailpit de verdade).
package tests

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"testing"
	"time"

	amqp "github.com/rabbitmq/amqp091-go"

	"github.com/MarceloVilela/microservices-go/platform/db"
	"github.com/MarceloVilela/microservices-go/platform/messages"
	"github.com/MarceloVilela/microservices-go/platform/topology"
)

var (
	nBinOnce sync.Once
	nBinPath string
	nBinErr  error
)

func notificationBinary(t *testing.T) string {
	t.Helper()
	nBinOnce.Do(func() {
		dir, err := os.MkdirTemp("", "ms-notification-bin")
		if err != nil {
			nBinErr = err
			return
		}
		nBinPath = filepath.Join(dir, "ms-notification")
		cmd := exec.Command("go", "build", "-o", nBinPath, "./cmd/ms-notification")
		cmd.Dir = filepath.Join("..", "ms-notification")
		if out, err := cmd.CombinedOutput(); err != nil {
			nBinErr = fmt.Errorf("go build: %w\n%s", err, out)
		}
	})
	if nBinErr != nil {
		t.Fatal(nBinErr)
	}
	return nBinPath
}

// ---- SMTP falso ----

type smtpSink struct {
	addr  string
	delay atomic.Int64 // ns de espera antes de aceitar o DATA
	mu    sync.Mutex
	got   []string
}

func newSMTPSink(t *testing.T) *smtpSink {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	s := &smtpSink{addr: ln.Addr().String()}
	t.Cleanup(func() { _ = ln.Close() })
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			go s.serve(c)
		}
	}()
	return s
}

func (s *smtpSink) serve(c net.Conn) {
	defer c.Close()
	r := bufio.NewReader(c)
	say := func(l string) { _, _ = c.Write([]byte(l + "\r\n")) }
	say("220 fake")
	for {
		line, err := r.ReadString('\n')
		if err != nil {
			return
		}
		switch strings.ToUpper(strings.TrimSpace(line)) {
		case "DATA":
			say("354 ok")
			var b strings.Builder
			for {
				l, err := r.ReadString('\n')
				if err != nil || l == ".\r\n" {
					break
				}
				b.WriteString(l)
			}
			time.Sleep(time.Duration(s.delay.Load()))
			s.mu.Lock()
			s.got = append(s.got, b.String())
			s.mu.Unlock()
			say("250 aceito")
		case "QUIT":
			say("221 tchau")
			return
		default:
			say("250 ok")
		}
	}
}

func (s *smtpSink) emails() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]string(nil), s.got...)
}

func (s *smtpSink) emailsFor(batchID string) int {
	n := 0
	for _, e := range s.emails() {
		if strings.Contains(e, batchID) {
			n++
		}
	}
	return n
}

// ---- processo ----

type notifProc struct {
	base string
	cmd  *exec.Cmd
	out  *lockedBuf
	done chan struct{}
}

// lockedBuf é um buffer que o processo escreve enquanto o teste lê.
type lockedBuf struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (l *lockedBuf) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.b.Write(p)
}

func (l *lockedBuf) String() string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.b.String()
}

func startNotification(t *testing.T, amqpURL, dbURL string, extra ...string) *notifProc {
	t.Helper()
	port := freePort(t)
	cmd := exec.Command(notificationBinary(t))
	cmd.Env = append(os.Environ(), "AMQP_URL="+amqpURL, "DATABASE_URL="+dbURL, "HTTP_ADDR=127.0.0.1:"+port,
		"READY_CACHE_TTL=100ms", "SHUTDOWN_TIMEOUT=5s", "LOG_LEVEL=debug")
	cmd.Env = append(cmd.Env, extra...)
	out := &lockedBuf{}
	cmd.Stderr, cmd.Stdout = out, out
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	n := &notifProc{base: "http://127.0.0.1:" + port, cmd: cmd, out: out, done: make(chan struct{})}
	go func() { _ = cmd.Wait(); close(n.done) }()
	t.Cleanup(func() {
		select {
		case <-n.done:
		default:
			_ = cmd.Process.Signal(syscall.SIGTERM)
			select {
			case <-n.done:
			case <-time.After(15 * time.Second):
				_ = cmd.Process.Kill()
				<-n.done
			}
		}
		if t.Failed() {
			t.Logf("log do ms-notification:\n%s", out.String())
		}
	})
	return n
}

func (n *notifProc) connected() bool {
	resp, err := http.Get(n.base + "/statusz")
	if err != nil {
		return false
	}
	defer resp.Body.Close()
	var v struct {
		Connected bool `json:"consumer_connected"`
	}
	return json.NewDecoder(resp.Body).Decode(&v) == nil && v.Connected
}

// logLines conta as linhas do log do serviço que contêm todos os trechos.
func (n *notifProc) logLines(parts ...string) int {
	c := 0
	for _, l := range strings.Split(n.out.String(), "\n") {
		ok := l != ""
		for _, p := range parts {
			ok = ok && strings.Contains(l, p)
		}
		if ok {
			c++
		}
	}
	return c
}

func publishCompleted(t *testing.T, url string, m messages.BatchCompleted) {
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
	body, _ := json.Marshal(m)
	conf, err := ch.PublishWithDeferredConfirmWithContext(context.Background(), topology.Exchange, topology.BatchCompleted.Key, true, false,
		amqp.Publishing{ContentType: "application/json", DeliveryMode: amqp.Persistent, MessageId: m.MessageID, Body: body})
	if err != nil {
		t.Fatal(err)
	}
	if ok, err := conf.WaitContext(context.Background()); err != nil || !ok {
		t.Fatalf("publicar: ok=%v err=%v", ok, err)
	}
}

func completedMsg() messages.BatchCompleted {
	return messages.BatchCompleted{V: messages.Version, MessageID: messages.NewID(), BatchID: messages.NewID(), Total: 4, Failed: 1}
}

func notifiedRows(t *testing.T, dbURL, batchID string) (n int, lastErr string) {
	t.Helper()
	p := pool(t, dbURL)
	if err := p.QueryRow(context.Background(),
		`SELECT count(*), coalesce(max(last_error), '') FROM notifications WHERE batch_id = $1`, batchID).Scan(&n, &lastErr); err != nil {
		t.Fatal(err)
	}
	return n, lastErr
}

func smtpEnv(addr string) []string {
	return []string{"SMTP_ADDR=" + addr, "SMTP_FROM=app@ms-go.local", "SMTP_TO=dev@ms-go.local", "SMTP_TIMEOUT=2s"}
}

// ---- testes ----

// Aceite: o mesmo batch.completed entregue duas vezes gera um registro, um e-mail e um log.
func TestNotificationReentregaGeraUmEfeito(t *testing.T) {
	url := setupBroker(t)
	dbURL, _ := migrated(t)
	sink := newSMTPSink(t)
	n := startNotification(t, url, dbURL, smtpEnv(sink.addr)...)
	eventually(t, 30*time.Second, "consumidor conectado", n.connected)

	m := completedMsg()
	publishCompleted(t, url, m)
	publishCompleted(t, url, m) // reentrega: mesma mensagem
	eventually(t, 30*time.Second, "fila esvaziada", func() bool { return queueMessages(t, url, topology.BatchCompleted.Name) == 0 })
	eventually(t, 30*time.Second, "e-mail enviado", func() bool { return sink.emailsFor(m.BatchID) >= 1 })
	time.Sleep(time.Second) // dá tempo de um eventual segundo e-mail aparecer

	if got := sink.emailsFor(m.BatchID); got != 1 {
		t.Errorf("e-mails = %d, quer 1", got)
	}
	if rows, lastErr := notifiedRows(t, dbURL, m.BatchID); rows != 1 || lastErr != "" {
		t.Errorf("notifications: linhas=%d last_error=%q", rows, lastErr)
	}
	if got := n.logLines(`"lote concluído"`, m.BatchID); got != 1 {
		t.Errorf("logs de notificação = %d, quer 1", got)
	}
}

// Aceite: com o SMTP fora do ar o fluxo continua e a falha fica registrada.
func TestNotificationSMTPForaNaoParaOConsumo(t *testing.T) {
	url := setupBroker(t)
	dbURL, _ := migrated(t)
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	deadAddr := ln.Addr().String()
	_ = ln.Close() // porta fechada
	n := startNotification(t, url, dbURL, smtpEnv(deadAddr)...)
	eventually(t, 30*time.Second, "consumidor conectado", n.connected)

	m1, m2 := completedMsg(), completedMsg()
	publishCompleted(t, url, m1)
	publishCompleted(t, url, m2)
	eventually(t, 30*time.Second, "os dois lotes consumidos", func() bool {
		r1, _ := notifiedRows(t, dbURL, m1.BatchID)
		r2, _ := notifiedRows(t, dbURL, m2.BatchID)
		return r1 == 1 && r2 == 1
	})
	if _, lastErr := notifiedRows(t, dbURL, m1.BatchID); !strings.Contains(lastErr, "smtp") {
		t.Errorf("last_error = %q, esperava a falha do smtp", lastErr)
	}
	if n.logLines(`"level":"ERROR"`, `"destino":"smtp"`) < 2 {
		t.Error("a falha do SMTP deveria estar em log de erro (uma por lote)")
	}
	if n.logLines(`"lote concluído"`, m1.BatchID) != 1 {
		t.Error("o slog deveria ter notificado mesmo com o SMTP fora")
	}
}

// Mensagem inválida não trava a fila: é descartada com log de erro e a seguinte passa.
func TestNotificationMensagemInvalidaNaoTravaAFila(t *testing.T) {
	url := setupBroker(t)
	dbURL, _ := migrated(t)
	n := startNotification(t, url, dbURL)
	eventually(t, 30*time.Second, "consumidor conectado", n.connected)

	publishCompleted(t, url, messages.BatchCompleted{V: 1, MessageID: messages.NewID(), BatchID: "nao-e-uuid", Total: 1})
	m := completedMsg()
	publishCompleted(t, url, m)
	eventually(t, 30*time.Second, "lote válido consumido", func() bool { r, _ := notifiedRows(t, dbURL, m.BatchID); return r == 1 })
	if n.logLines(`"level":"ERROR"`, "inválida") != 1 {
		t.Error("esperava um log de erro para a mensagem inválida")
	}
}

// O mesmo processo volta a consumir depois que o broker some e volta.
func TestNotificationReconectaSozinhaQuandoOBrokerCaiEVolta(t *testing.T) {
	realURL, dbURL := setupBroker(t), freshDB(t)
	conn, err := amqp.ParseURI(realURL)
	if err != nil {
		t.Fatal(err)
	}
	px := newProxy(t, net.JoinHostPort(conn.Host, fmt.Sprint(conn.Port)))
	viaProxy := fmt.Sprintf("amqp://%s:%s@%s/", conn.Username, conn.Password, px.addr)
	if err := db.Migrate(context.Background(), pool(t, dbURL)); err != nil {
		t.Fatal(err)
	}
	n := startNotification(t, viaProxy, dbURL, "CONSUMER_BACKOFF_MAX=2s")
	eventually(t, 30*time.Second, "consumidor conectado", n.connected)

	px.Stop()
	eventually(t, 15*time.Second, "percebe a queda", func() bool { return !n.connected() })
	m := completedMsg()
	publishCompleted(t, realURL, m)
	select {
	case <-n.done:
		t.Fatal("o serviço saiu com o broker fora (deveria esperar)")
	case <-time.After(time.Second):
	}
	px.Start(t)
	eventually(t, 30*time.Second, "o mesmo processo reconecta", n.connected)
	eventually(t, 30*time.Second, "lote notificado", func() bool { r, _ := notifiedRows(t, dbURL, m.BatchID); return r == 1 })
}

func TestNotificationSIGTERMSaiCom0(t *testing.T) {
	url := setupBroker(t)
	dbURL, _ := migrated(t)
	n := startNotification(t, url, dbURL)
	eventually(t, 30*time.Second, "consumidor conectado", n.connected)
	start := time.Now()
	_ = n.cmd.Process.Signal(syscall.SIGTERM)
	select {
	case <-n.done:
	case <-time.After(10 * time.Second):
		t.Fatal("não encerrou")
	}
	if code := n.cmd.ProcessState.ExitCode(); code != 0 {
		t.Errorf("saída = %d, quer 0", code)
	}
	if d := time.Since(start); d > 6*time.Second {
		t.Errorf("encerrou em %s", d)
	}
}

func TestNotificationSMTPSemDestinatarioNaoSobe(t *testing.T) {
	url := setupBroker(t)
	dbURL, _ := migrated(t)
	n := startNotification(t, url, dbURL, "SMTP_ADDR=127.0.0.1:1", "SMTP_FROM=a@b")
	select {
	case <-n.done:
	case <-time.After(15 * time.Second):
		t.Fatal("deveria ter saído por configuração inválida")
	}
	if code := n.cmd.ProcessState.ExitCode(); code != 1 {
		t.Errorf("saída = %d, quer 1", code)
	}
}

// Duas réplicas recebendo o mesmo batch.completed ao mesmo tempo, com SMTP lento: um só e-mail
// (o advisory lock serializa; a segunda réplica encontra a linha gravada).
func TestNotificationDuasReplicasUmSoEmail(t *testing.T) {
	url := setupBroker(t)
	dbURL, _ := migrated(t)
	sink := newSMTPSink(t)
	sink.delay.Store(int64(1500 * time.Millisecond))
	a := startNotification(t, url, dbURL, smtpEnv(sink.addr)...)
	b := startNotification(t, url, dbURL, smtpEnv(sink.addr)...)
	eventually(t, 30*time.Second, "réplicas conectadas", func() bool { return a.connected() && b.connected() })

	m := completedMsg()
	publishCompleted(t, url, m)
	publishCompleted(t, url, m)
	eventually(t, 30*time.Second, "fila esvaziada", func() bool { return queueMessages(t, url, topology.BatchCompleted.Name) == 0 })
	eventually(t, 30*time.Second, "e-mail enviado", func() bool { return sink.emailsFor(m.BatchID) >= 1 })
	time.Sleep(3 * time.Second)
	if got := sink.emailsFor(m.BatchID); got != 1 {
		t.Errorf("e-mails = %d, quer 1", got)
	}
	if rows, _ := notifiedRows(t, dbURL, m.BatchID); rows != 1 {
		t.Errorf("linhas = %d, quer 1", rows)
	}
}

// batch.completed com versão desconhecida do contrato é descartada com log de erro.
func TestNotificationVersaoDesconhecidaEDescartada(t *testing.T) {
	url := setupBroker(t)
	dbURL, _ := migrated(t)
	n := startNotification(t, url, dbURL)
	eventually(t, 30*time.Second, "consumidor conectado", n.connected)
	m := completedMsg()
	m.V = 2
	publishCompleted(t, url, m)
	eventually(t, 30*time.Second, "descartada", func() bool { return n.logLines(`"level":"ERROR"`, "versão 2") == 1 })
	if rows, _ := notifiedRows(t, dbURL, m.BatchID); rows != 0 {
		t.Error("não deveria registrar lote de versão desconhecida")
	}
}
