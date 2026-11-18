// Comando httpsprobe: baixa uma URL HTTPS (seguindo redirects) e confere que
// veio 200 com imagem. Roda na mesma base distroless da imagem final para
// provar que o bundle de CA está presente (ADR 0011, S5).
package main

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"
)

func main() {
	if len(os.Args) != 2 {
		fmt.Fprintln(os.Stderr, "uso: httpsprobe <url>")
		os.Exit(2)
	}
	client := &http.Client{Timeout: 20 * time.Second}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	//nolint:gosec // sonda de linha de comando: a URL vem do operador, não de entrada de rede
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, os.Args[1], nil)
	if err != nil {
		fmt.Fprintf(os.Stderr, "URL inválida: %v\n", err)
		os.Exit(2)
	}
	resp, err := client.Do(req) //nolint:gosec // idem: URL do operador
	if err != nil {
		fmt.Fprintf(os.Stderr, "falha: %v\n", err)
		os.Exit(1)
	}
	defer func() { _ = resp.Body.Close() }()
	n, err := io.Copy(io.Discard, io.LimitReader(resp.Body, 10<<20))
	ct := resp.Header.Get("Content-Type")
	if err != nil || resp.StatusCode != http.StatusOK || n == 0 || !strings.HasPrefix(ct, "image/") {
		fmt.Fprintf(os.Stderr, "inesperado: status=%d bytes=%d content-type=%q err=%v\n", resp.StatusCode, n, ct, err)
		os.Exit(1)
	}
	fmt.Printf("ok: status=%d bytes=%d content-type=%s host-final=%s\n", resp.StatusCode, n, ct, resp.Request.URL.Host)
}
