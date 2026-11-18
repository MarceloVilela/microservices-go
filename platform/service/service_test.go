package service_test

import (
	"bytes"
	"context"
	"errors"
	"net"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/MarceloVilela/microservices-go/platform/service"
)

func freeAddr(t *testing.T) string {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	return l.Addr().String()
}

func spec(v func(context.Context, string) error) service.Spec {
	return service.Spec{Name: "ms-teste", DefaultAddr: ":0", Verifier: v}
}

func okVerifier(context.Context, string) error { return nil }

func TestSubcomandoDesconhecidoSai2(t *testing.T) {
	var out, errb bytes.Buffer
	if code := service.Run(context.Background(), spec(okVerifier), []string{"xyz"}, nil, &out, &errb); code != 2 {
		t.Errorf("código = %d", code)
	}
}

func TestConfigCheck(t *testing.T) {
	var out, errb bytes.Buffer
	code := service.Run(context.Background(), spec(okVerifier), []string{"config", "check"}, []string{"AMQP_URL=amqp://u:segredo@h/"}, &out, &errb)
	if code != 0 {
		t.Fatalf("código = %d, stderr = %s", code, errb.String())
	}
	if strings.Contains(out.String(), "segredo") || !strings.Contains(out.String(), "AMQP_URL") {
		t.Errorf("saída deve listar nomes e nunca valores: %q", out.String())
	}
	out.Reset()
	if code := service.Run(context.Background(), spec(okVerifier), []string{"config", "check"}, nil, &out, &errb); code != 1 {
		t.Errorf("sem AMQP_URL deveria sair 1, saiu %d", code)
	}
}

func TestHealthcheck(t *testing.T) {
	addr := freeAddr(t)
	env := []string{"AMQP_URL=amqp://x", "HTTP_ADDR=" + addr}
	var out, errb bytes.Buffer
	if code := service.Run(context.Background(), spec(okVerifier), []string{"healthcheck"}, env, &out, &errb); code != 1 {
		t.Errorf("sem servidor deveria sair 1, saiu %d", code)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan int, 1)
	go func() { done <- service.Run(ctx, spec(okVerifier), nil, env, &bytes.Buffer{}, &bytes.Buffer{}) }()
	waitUp(t, addr)
	if code := service.Run(context.Background(), spec(okVerifier), []string{"healthcheck"}, env, &out, &errb); code != 0 {
		t.Errorf("com servidor deveria sair 0, saiu %d (%s)", code, errb.String())
	}
	cancel()
	if code := <-done; code != 0 {
		t.Errorf("shutdown gracioso deveria sair 0, saiu %d", code)
	}
}

func TestServeReadyzSegueOVerifier(t *testing.T) {
	addr := freeAddr(t)
	var fail atomic.Bool
	fail.Store(true)
	v := func(context.Context, string) error {
		if fail.Load() {
			return errors.New("fila ausente")
		}
		return nil
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan int, 1)
	go func() {
		done <- service.Run(ctx, spec(v), nil, []string{"AMQP_URL=amqp://x", "HTTP_ADDR=" + addr, "READY_CACHE_TTL=1ms"}, &bytes.Buffer{}, &bytes.Buffer{})
	}()
	defer func() {
		cancel()
		if code := <-done; code != 0 {
			t.Errorf("Run deveria encerrar com 0, saiu %d", code)
		}
	}()
	waitUp(t, addr)
	if c := status(t, "http://"+addr+"/readyz"); c != 503 {
		t.Errorf("readyz com divergência = %d", c)
	}
	fail.Store(false)
	time.Sleep(10 * time.Millisecond) // passa o TTL do cache
	if c := status(t, "http://"+addr+"/readyz"); c != 200 {
		t.Errorf("readyz ok = %d", c)
	}
}

func TestServeConfigInvalidaSai1(t *testing.T) {
	var errb bytes.Buffer
	if code := service.Run(context.Background(), spec(okVerifier), nil, nil, &bytes.Buffer{}, &errb); code != 1 {
		t.Errorf("sem AMQP_URL deveria sair 1, saiu %d", code)
	}
}

func status(t *testing.T, url string) int {
	t.Helper()
	client := &http.Client{Timeout: 5 * time.Second}
	resp, err := client.Get(url)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	return resp.StatusCode
}

func waitUp(t *testing.T, addr string) {
	t.Helper()
	for range 100 {
		if c, err := net.DialTimeout("tcp", addr, 50*time.Millisecond); err == nil {
			c.Close()
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal("servidor não subiu")
}

func TestReadyzEmCacheNaoRepeteAVerificacao(t *testing.T) {
	addr := freeAddr(t)
	var calls atomic.Int32
	v := func(context.Context, string) error { calls.Add(1); return nil }
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan int, 1)
	go func() {
		done <- service.Run(ctx, spec(v), nil, []string{"AMQP_URL=amqp://x", "HTTP_ADDR=" + addr, "READY_CACHE_TTL=1h"}, &bytes.Buffer{}, &bytes.Buffer{})
	}()
	defer func() { cancel(); <-done }()
	waitUp(t, addr)
	for range 5 {
		if c := status(t, "http://"+addr+"/readyz"); c != 200 {
			t.Fatalf("readyz = %d", c)
		}
	}
	if n := calls.Load(); n != 1 {
		t.Errorf("verificação chamada %d vezes; com cache deveria ser 1", n)
	}
}

func TestHTTPAddrInvalidaFalhaNoConfigCheck(t *testing.T) {
	for _, bad := range []string{"abc", ":porta", ":70000", "host:", "1:2:3"} {
		var out, errb bytes.Buffer
		code := service.Run(context.Background(), spec(okVerifier), []string{"config", "check"}, []string{"AMQP_URL=amqp://x", "HTTP_ADDR=" + bad}, &out, &errb)
		if code != 1 {
			t.Errorf("HTTP_ADDR=%q deveria sair 1, saiu %d", bad, code)
		}
	}
	var out, errb bytes.Buffer
	for _, good := range []string{":8081", "127.0.0.1:0", "[::1]:8081", "localhost:80"} {
		if code := service.Run(context.Background(), spec(okVerifier), []string{"config", "check"}, []string{"AMQP_URL=amqp://x", "HTTP_ADDR=" + good}, &out, &errb); code != 0 {
			t.Errorf("HTTP_ADDR=%q deveria sair 0, saiu %d (%s)", good, code, errb.String())
		}
	}
}

func TestHealthcheckUsaOHostConfigurado(t *testing.T) {
	l, err := net.Listen("tcp", "[::1]:0")
	if err != nil {
		t.Skip("sem IPv6 de loopback")
	}
	addr := l.Addr().String()
	_ = l.Close()
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan int, 1)
	env := []string{"AMQP_URL=amqp://x", "HTTP_ADDR=" + addr}
	go func() { done <- service.Run(ctx, spec(okVerifier), nil, env, &bytes.Buffer{}, &bytes.Buffer{}) }()
	defer func() { cancel(); <-done }()
	waitUp(t, addr)
	var errb bytes.Buffer
	if code := service.Run(context.Background(), spec(okVerifier), []string{"healthcheck"}, env, &bytes.Buffer{}, &errb); code != 0 {
		t.Errorf("healthcheck em %s deveria sair 0, saiu %d (%s)", addr, code, errb.String())
	}
}
