package httpfetch_test

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"strings"
	"testing"
	"time"

	"github.com/MarceloVilela/microservices-go/ms-worker/internal/adapters/httpfetch"
	"github.com/MarceloVilela/microservices-go/ms-worker/internal/domain"
)

func relaxed(hosts ...string) httpfetch.Config {
	return httpfetch.Config{AllowedHosts: hosts, Timeout: 2 * time.Second, MaxBytes: 1 << 10, MaxRedirects: 3,
		AllowAnyPort: true, AllowHTTP: true, AllowPrivateIP: true}
}

func class(t *testing.T, err error) (domain.Class, string) {
	t.Helper()
	var f *domain.Failure
	if !errors.As(err, &f) {
		t.Fatalf("erro sem classe: %v", err)
	}
	return f.Class, f.Reason
}

func wantFailure(t *testing.T, err error, c domain.Class, reason string) {
	t.Helper()
	if err == nil {
		t.Fatalf("esperava %s %q, obtive sucesso", c, reason)
	}
	if gc, gr := class(t, err); gc != c || gr != reason {
		t.Errorf("falha = %s %q, esperado %s %q", gc, gr, c, reason)
	}
}

func TestSucesso(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte("imagem")) }))
	defer srv.Close()
	got, err := httpfetch.New(relaxed("127.0.0.1")).Fetch(context.Background(), srv.URL+"/a.jpg")
	if err != nil || string(got) != "imagem" {
		t.Fatalf("got=%q err=%v", got, err)
	}
}

func TestStatusHTTP(t *testing.T) {
	for _, c := range []struct {
		code   int
		class  domain.Class
		reason string
	}{
		{404, domain.Permanent, "HTTP 404"}, {403, domain.Permanent, "HTTP 403"}, {400, domain.Permanent, "HTTP 400"},
		{500, domain.Transient, "HTTP 500"}, {503, domain.Transient, "HTTP 503"}, {429, domain.Transient, "HTTP 429"}, {408, domain.Transient, "HTTP 408"},
	} {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(c.code) }))
		_, err := httpfetch.New(relaxed("127.0.0.1")).Fetch(context.Background(), srv.URL)
		srv.Close()
		wantFailure(t, err, c.class, c.reason)
	}
}

func TestPoliticaDeURL(t *testing.T) {
	f := httpfetch.New(httpfetch.Config{AllowedHosts: []string{"picsum.photos"}, Timeout: time.Second, MaxBytes: 1, MaxRedirects: 3})
	for url, reason := range map[string]string{
		"http://picsum.photos/a.jpg":         "esquema não permitido",
		"ftp://picsum.photos/a.jpg":          "esquema não permitido",
		"https://user:pw@picsum.photos/a":    "URL com credencial",
		"https://picsum.photos:8443/a":       "porta não permitida",
		"https://example.com/a.jpg":          "host fora da allowlist",
		"https://picsum.photos.evil.com/a":   "host fora da allowlist",
		"https://127.0.0.1/a":                "host fora da allowlist",
		"https:///sem-host":                  "URL sem host",
		"https://[::1]/a":                    "host fora da allowlist",
		"%zz":                                "URL inválida",
		"https://picsum.photos@evil.com/a":   "URL com credencial",
		"https://PICSUM.photos.evil.com/a.j": "host fora da allowlist",
	} {
		_, err := f.Fetch(context.Background(), url)
		wantFailure(t, err, domain.Permanent, reason)
	}
}

func TestRedirectParaHostForaDaAllowlist(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "http://evil.example/x.jpg", http.StatusFound)
	}))
	defer srv.Close()
	_, err := httpfetch.New(relaxed("127.0.0.1")).Fetch(context.Background(), srv.URL)
	wantFailure(t, err, domain.Permanent, "host fora da allowlist")
}

func TestRedirectDentroDaAllowlistSegue(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/final" {
			_, _ = w.Write([]byte("ok"))
			return
		}
		http.Redirect(w, r, "/final", http.StatusFound)
	}))
	defer srv.Close()
	got, err := httpfetch.New(relaxed("127.0.0.1")).Fetch(context.Background(), srv.URL+"/inicio")
	if err != nil || string(got) != "ok" {
		t.Fatalf("got=%q err=%v", got, err)
	}
}

func TestRedirectsDemais(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "/de-novo", http.StatusFound)
	}))
	defer srv.Close()
	_, err := httpfetch.New(relaxed("127.0.0.1")).Fetch(context.Background(), srv.URL)
	wantFailure(t, err, domain.Permanent, "redirects demais")
}

func TestLimiteDeBytes(t *testing.T) {
	big := strings.Repeat("x", 4<<10)
	for name, h := range map[string]http.HandlerFunc{
		"com Content-Length": func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte(big)) },
		"em chunks (sem Content-Length)": func(w http.ResponseWriter, _ *http.Request) {
			for range 4 {
				_, _ = w.Write([]byte(big[:1<<10]))
				w.(http.Flusher).Flush()
			}
		},
	} {
		srv := httptest.NewServer(h)
		_, err := httpfetch.New(relaxed("127.0.0.1")).Fetch(context.Background(), srv.URL)
		srv.Close()
		if err == nil {
			t.Errorf("%s: esperava falha", name)
			continue
		}
		wantFailure(t, err, domain.Permanent, "imagem acima do limite de bytes")
	}
}

func TestTimeoutEhTransitorio(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-r.Context().Done():
		case <-time.After(3 * time.Second):
		}
	}))
	defer srv.Close()
	cfg := relaxed("127.0.0.1")
	cfg.Timeout = 100 * time.Millisecond
	_, err := httpfetch.New(cfg).Fetch(context.Background(), srv.URL)
	if err == nil {
		t.Fatal("esperava erro")
	}
	if c, _ := class(t, err); c != domain.Transient {
		t.Errorf("classe = %s", c)
	}
}

func TestServidorQueFechaAConexaoEhTransitorio(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		c, _, _ := w.(http.Hijacker).Hijack()
		_ = c.Close()
	}))
	defer srv.Close()
	_, err := httpfetch.New(relaxed("127.0.0.1")).Fetch(context.Background(), srv.URL)
	if err == nil {
		t.Fatal("esperava erro")
	}
	if c, _ := class(t, err); c != domain.Transient {
		t.Errorf("classe = %s", c)
	}
}

func TestContextoCanceladoEhTransitorio(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { <-r.Context().Done() }))
	defer srv.Close()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := httpfetch.New(relaxed("127.0.0.1")).Fetch(ctx, srv.URL)
	if err == nil {
		t.Fatal("esperava erro")
	}
	if c, _ := class(t, err); c != domain.Transient {
		t.Errorf("classe = %s", c)
	}
}

// O IP é checado na conexão: com a allowlist satisfeita (127.0.0.1) e HTTPS válido, o
// loopback ainda é recusado quando AllowPrivateIP está desligado (o padrão de produção).
func TestIPDeLoopbackEhBloqueadoNaConexao(t *testing.T) {
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte("segredo interno")) }))
	defer srv.Close()
	pool := x509.NewCertPool()
	pool.AddCert(srv.Certificate())
	cfg := relaxed("127.0.0.1")
	cfg.AllowPrivateIP, cfg.AllowHTTP = false, false
	cfg.TLS = &tls.Config{RootCAs: pool, MinVersion: tls.VersionTLS12}
	_, err := httpfetch.New(cfg).Fetch(context.Background(), srv.URL)
	wantFailure(t, err, domain.Permanent, "endereço IP bloqueado")

	// controle: com AllowPrivateIP o mesmo servidor responde (a recusa acima foi mesmo do IP)
	cfg.AllowPrivateIP = true
	got, err := httpfetch.New(cfg).Fetch(context.Background(), srv.URL)
	if err != nil || string(got) != "segredo interno" {
		t.Fatalf("controle: got=%q err=%v", got, err)
	}
}

func TestBlockedCobreAsRedesInternas(t *testing.T) {
	for _, s := range []string{"127.0.0.1", "10.1.2.3", "172.16.0.1", "192.168.1.1", "169.254.169.254", "100.64.0.1", "0.0.0.0",
		"::1", "fe80::1", "fc00::1", "::ffff:127.0.0.1", "::ffff:10.0.0.1", "224.0.0.1"} {
		if !httpfetch.Blocked(netip.MustParseAddr(s)) {
			t.Errorf("%s deveria ser bloqueado", s)
		}
	}
	for _, s := range []string{"8.8.8.8", "151.101.1.1", "2606:4700::1111"} {
		if httpfetch.Blocked(netip.MustParseAddr(s)) {
			t.Errorf("%s não deveria ser bloqueado", s)
		}
	}
}

func TestFalhaDeDNSEhTransitoriaENaoAlteraAConfig(t *testing.T) {
	hosts := []string{"  NAO-EXISTE.invalid "}
	cfg := relaxed()
	cfg.AllowedHosts = hosts
	cfg.AllowHTTP = true
	_, err := httpfetch.New(cfg).Fetch(context.Background(), "http://nao-existe.invalid/a.jpg")
	if err == nil {
		t.Fatal("esperava erro")
	}
	if c, _ := class(t, err); c != domain.Transient {
		t.Errorf("classe = %s, esperado transient", c)
	}
	if hosts[0] != "  NAO-EXISTE.invalid " {
		t.Errorf("a slice do chamador foi alterada: %q", hosts[0])
	}
}
