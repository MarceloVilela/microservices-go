package smtpn_test

import (
	"bufio"
	"context"
	"net"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/MarceloVilela/microservices-go/ms-notification/internal/adapters/smtpn"
	"github.com/MarceloVilela/microservices-go/platform/messages"
)

// fakeSMTP é o mínimo de SMTP que net/smtp precisa. rejectRcpt recusa o destinatário; hang
// aceita a conexão e nunca responde.
type fakeSMTP struct {
	addr       string
	rejectRcpt atomic.Bool
	hang       atomic.Bool
	quitDrops  atomic.Bool
	mu         sync.Mutex
	got        []string
}

func newFake(t *testing.T) *fakeSMTP {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	f := &fakeSMTP{addr: ln.Addr().String()}
	t.Cleanup(func() { _ = ln.Close() })
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			go f.serve(c)
		}
	}()
	return f
}

func (f *fakeSMTP) serve(c net.Conn) {
	defer func() { _ = c.Close() }()
	if f.hang.Load() {
		time.Sleep(3 * time.Second)
		return
	}
	r := bufio.NewReader(c)
	say := func(s string) { _, _ = c.Write([]byte(s + "\r\n")) }
	say("220 fake ESMTP")
	for {
		line, err := r.ReadString('\n')
		if err != nil {
			return
		}
		cmd := strings.ToUpper(strings.TrimSpace(line))
		switch {
		case strings.HasPrefix(cmd, "EHLO"), strings.HasPrefix(cmd, "HELO"):
			say("250 fake")
		case strings.HasPrefix(cmd, "MAIL"):
			say("250 ok")
		case strings.HasPrefix(cmd, "RCPT"):
			if f.rejectRcpt.Load() {
				say("550 sem caixa")
			} else {
				say("250 ok")
			}
		case cmd == "DATA":
			say("354 envie")
			var b strings.Builder
			for {
				l, err := r.ReadString('\n')
				if err != nil || l == ".\r\n" {
					break
				}
				b.WriteString(l)
			}
			f.mu.Lock()
			f.got = append(f.got, b.String())
			f.mu.Unlock()
			say("250 aceito")
		case cmd == "QUIT":
			if f.quitDrops.Load() {
				return // derruba a conexão sem responder
			}
			say("221 tchau")
			return
		default:
			say("250 ok")
		}
	}
}

func notifier(addr string) smtpn.Notifier {
	return smtpn.Notifier{Addr: addr, From: "app@ms-go.local", To: "dev@ms-go.local", Timeout: time.Second}
}

func TestEnviaUmEmailComOsTotais(t *testing.T) {
	f := newFake(t)
	err := notifier(f.addr).Notify(context.Background(), messages.BatchCompleted{BatchID: "b-123", Total: 4, Failed: 1})
	if err != nil {
		t.Fatal(err)
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.got) != 1 {
		t.Fatalf("e-mails = %d", len(f.got))
	}
	for _, want := range []string{"From: app@ms-go.local", "To: dev@ms-go.local", "b-123", "Fotos: 4", "Processadas: 3", "Falhas: 1"} {
		if !strings.Contains(f.got[0], want) {
			t.Errorf("mensagem sem %q:\n%s", want, f.got[0])
		}
	}
}

func TestPortaFechadaDevolveErro(t *testing.T) {
	ln, _ := net.Listen("tcp", "127.0.0.1:0")
	addr := ln.Addr().String()
	_ = ln.Close()
	if err := notifier(addr).Notify(context.Background(), messages.BatchCompleted{BatchID: "b"}); err == nil {
		t.Fatal("esperava erro com a porta fechada")
	}
}

func TestDestinatarioRecusadoDevolveErro(t *testing.T) {
	f := newFake(t)
	f.rejectRcpt.Store(true)
	if err := notifier(f.addr).Notify(context.Background(), messages.BatchCompleted{BatchID: "b"}); err == nil {
		t.Fatal("esperava erro com RCPT recusado")
	}
}

func TestServidorMudoEstouraOPrazo(t *testing.T) {
	f := newFake(t)
	f.hang.Store(true)
	start := time.Now()
	err := notifier(f.addr).Notify(context.Background(), messages.BatchCompleted{BatchID: "b"})
	if err == nil {
		t.Fatal("esperava erro por prazo")
	}
	if time.Since(start) > 2*time.Second {
		t.Errorf("demorou %v; o Timeout de 1 s não valeu", time.Since(start))
	}
}

func TestQuebraDeLinhaNoEnderecoERecusada(t *testing.T) {
	n := notifier("127.0.0.1:1")
	n.To = "a@b\r\nBcc: x@y"
	if err := n.Notify(context.Background(), messages.BatchCompleted{BatchID: "b"}); err == nil {
		t.Fatal("esperava recusa de cabeçalho injetado")
	}
}

func TestErroNoQuitDepoisDoDataAceitoNaoEFalha(t *testing.T) {
	f := newFake(t)
	f.quitDrops.Store(true)
	if err := notifier(f.addr).Notify(context.Background(), messages.BatchCompleted{BatchID: "b"}); err != nil {
		t.Fatalf("o e-mail foi aceito; erro no QUIT não é falha: %v", err)
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.got) != 1 {
		t.Errorf("e-mails = %d", len(f.got))
	}
}
