// Spike S5: binário único com subcomandos serve, download e healthcheck, para testar a imagem final
// (HTTPS sem curl/shell, healthcheck pelo próprio binário).
package main

import (
	"fmt"
	"io"
	"net/http"
	"os"
	"time"
)

func main() {
	if len(os.Args) < 2 {
		fmt.Fprintln(os.Stderr, "uso: app serve | download URL | healthcheck")
		os.Exit(2)
	}
	switch os.Args[1] {
	case "serve":
		mux := http.NewServeMux()
		mux.HandleFunc("/healthz", func(w http.ResponseWriter, _ *http.Request) { w.Write([]byte("ok")) })
		fmt.Println("ouvindo em :8080")
		if err := http.ListenAndServe(":8080", mux); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
	case "download":
		c := &http.Client{Timeout: 20 * time.Second}
		resp, err := c.Get(os.Args[2])
		if err != nil {
			fmt.Println("ERRO:", err)
			os.Exit(1)
		}
		defer resp.Body.Close()
		n, _ := io.Copy(io.Discard, resp.Body)
		fmt.Printf("status=%d final=%s tipo=%s bytes=%d\n", resp.StatusCode, resp.Request.URL.Host, resp.Header.Get("Content-Type"), n)
	case "healthcheck":
		c := &http.Client{Timeout: 2 * time.Second}
		resp, err := c.Get("http://127.0.0.1:8080/healthz")
		if err != nil || resp.StatusCode != 200 {
			fmt.Println("unhealthy:", err)
			os.Exit(1)
		}
		fmt.Println("healthy")
	}
}
