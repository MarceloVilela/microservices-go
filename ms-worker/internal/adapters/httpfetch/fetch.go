// Package httpfetch baixa a imagem com as defesas de SSRF da ADR 0010 / N4: allowlist de hosts,
// só https na porta 443, revalidação a cada redirect, checagem do IP **resolvido** no momento
// da conexão (cobre DNS rebinding), prazo total e limite de bytes.
package httpfetch

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"slices"
	"strings"
	"syscall"
	"time"

	"github.com/MarceloVilela/microservices-go/ms-worker/internal/domain"
)

// Config da busca. Os três Allow* existem só para o teste (servidor local em loopback, HTTP);
// em produção ficam false e o main só os liga com a build faultinject.
type Config struct {
	AllowedHosts []string
	Timeout      time.Duration
	MaxBytes     int64
	MaxRedirects int
	// AllowAnyPort libera portas diferentes de 443.
	AllowAnyPort bool
	// AllowHTTP libera o esquema http.
	AllowHTTP bool
	// AllowPrivateIP libera loopback e redes privadas.
	AllowPrivateIP bool
	// TLS é a configuração do cliente (testes injetam a CA do servidor de teste).
	TLS *tls.Config
}

// Fetcher implementa app.Fetcher.
type Fetcher struct {
	cfg    Config
	client *http.Client
}

var errBlockedIP = errors.New("endereço IP bloqueado")

// New monta o Fetcher.
func New(cfg Config) *Fetcher {
	f := &Fetcher{cfg: cfg}
	f.cfg.AllowedHosts = slices.Clone(cfg.AllowedHosts) // não altera o slice do chamador
	for i, h := range f.cfg.AllowedHosts {
		f.cfg.AllowedHosts[i] = strings.ToLower(strings.TrimSpace(h))
	}
	dialer := &net.Dialer{
		Timeout: 5 * time.Second,
		// Control roda com o IP já resolvido, imediatamente antes do connect: o IP validado
		// é o IP usado, então um DNS que troca de resposta não passa.
		Control: func(_, address string, _ syscall.RawConn) error {
			if cfg.AllowPrivateIP {
				return nil
			}
			ap, err := netip.ParseAddrPort(address)
			if err != nil {
				return fmt.Errorf("%w: %q", errBlockedIP, address)
			}
			if blocked(ap.Addr()) {
				return errBlockedIP
			}
			return nil
		},
	}
	tr := &http.Transport{
		Proxy:                 nil, // nunca por proxy de ambiente: ele contornaria a checagem do IP
		DialContext:           dialer.DialContext,
		TLSClientConfig:       cfg.TLS,
		TLSHandshakeTimeout:   5 * time.Second,
		ResponseHeaderTimeout: cfg.Timeout,
		DisableKeepAlives:     false,
		MaxIdleConns:          16,
		IdleConnTimeout:       30 * time.Second,
		ForceAttemptHTTP2:     true,
	}
	f.client = &http.Client{
		Transport: tr,
		Timeout:   cfg.Timeout,
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if len(via) > cfg.MaxRedirects {
				return domain.NewPermanent("redirects demais", nil)
			}
			return f.checkURL(req.URL)
		},
	}
	return f
}

// blocked diz se o IP é de rede interna (loopback, privada, link-local, multicast, CGNAT, não especificado).
func blocked(ip netip.Addr) bool {
	ip = ip.Unmap()
	return !ip.IsValid() || ip.IsLoopback() || ip.IsPrivate() || ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() ||
		ip.IsMulticast() || ip.IsUnspecified() || cgnat.Contains(ip)
}

var cgnat = netip.MustParsePrefix("100.64.0.0/10")

// checkURL aplica a política de URL; devolve falha definitiva se violar.
func (f *Fetcher) checkURL(u *url.URL) error {
	switch {
	case u.Scheme != "https" && (!f.cfg.AllowHTTP || u.Scheme != "http"):
		return domain.NewPermanent("esquema não permitido", nil)
	case u.User != nil:
		return domain.NewPermanent("URL com credencial", nil)
	case u.Hostname() == "":
		return domain.NewPermanent("URL sem host", nil)
	}
	if p := u.Port(); p != "" && p != "443" && !f.cfg.AllowAnyPort {
		return domain.NewPermanent("porta não permitida", nil)
	}
	if !slices.Contains(f.cfg.AllowedHosts, strings.ToLower(u.Hostname())) {
		return domain.NewPermanent("host fora da allowlist", nil)
	}
	return nil
}

// Fetch baixa sourceURL. Os erros vêm como *domain.Failure; o motivo nunca inclui a URL.
func (f *Fetcher) Fetch(ctx context.Context, sourceURL string) ([]byte, error) {
	u, err := url.Parse(sourceURL)
	if err != nil {
		return nil, domain.NewPermanent("URL inválida", nil)
	}
	if err := f.checkURL(u); err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return nil, domain.NewPermanent("URL inválida", nil)
	}
	req.Header.Set("Accept", "image/jpeg,image/png")
	resp, err := f.client.Do(req)
	if err != nil {
		return nil, mapDoErr(err)
	}
	defer func() { _ = resp.Body.Close() }()

	if err := statusErr(resp.StatusCode); err != nil {
		return nil, err
	}
	if resp.ContentLength > f.cfg.MaxBytes {
		return nil, domain.NewPermanent("imagem acima do limite de bytes", nil)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, f.cfg.MaxBytes+1))
	if err != nil {
		return nil, domain.NewTransient("erro ao ler a resposta", err)
	}
	if int64(len(body)) > f.cfg.MaxBytes {
		return nil, domain.NewPermanent("imagem acima do limite de bytes", nil)
	}
	return body, nil
}

func statusErr(code int) error {
	reason := fmt.Sprintf("HTTP %d", code)
	switch {
	case code == http.StatusOK:
		return nil
	case code == http.StatusRequestTimeout || code == http.StatusTooManyRequests || code >= 500:
		return domain.NewTransient(reason, nil)
	default: // 4xx (404, 403...), 3xx sem Location e afins: tentar de novo não muda nada
		return domain.NewPermanent(reason, nil)
	}
}

func mapDoErr(err error) error {
	var f *domain.Failure
	switch {
	case errors.As(err, &f): // veio do CheckRedirect
		return f
	case errors.Is(err, errBlockedIP):
		return domain.NewPermanent("endereço IP bloqueado", nil)
	case errors.Is(err, context.DeadlineExceeded):
		return domain.NewTransient("timeout", err)
	case errors.Is(err, context.Canceled):
		return domain.NewTransient("cancelado", err)
	}
	// "host não resolve" é transitório: o host passou pela allowlist (é conhecido), e o resolvedor
	// do Docker devolve NXDOMAIN/resposta vazia em falhas passageiras
	var dns *net.DNSError
	if errors.As(err, &dns) {
		return domain.NewTransient("falha de DNS", err)
	}
	return domain.NewTransient("erro de rede", err)
}
