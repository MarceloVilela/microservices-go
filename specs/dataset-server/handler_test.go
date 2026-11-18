package main

import (
	"bytes"
	"image"
	_ "image/jpeg"
	_ "image/png"
	"io"
	"net/http"
	"net/http/httptest"
	"runtime"
	"testing"
)

func get(t *testing.T, url string) (*http.Response, []byte) {
	t.Helper()
	r, err := http.Get(url)
	if err != nil {
		t.Fatal(err)
	}
	defer r.Body.Close()
	b, _ := io.ReadAll(r.Body)
	return r, b
}

// caso 9: poucos KB, 900 megapixels; DecodeConfig lê só o cabeçalho (sem decodificar a imagem).
func TestBigPixels(t *testing.T) {
	ts := httptest.NewServer(New().Handler())
	defer ts.Close()
	_, b := get(t, ts.URL+"/big-pixels.png")
	var before, after runtime.MemStats
	runtime.ReadMemStats(&before)
	cfg, format, err := image.DecodeConfig(bytes.NewReader(b))
	runtime.ReadMemStats(&after)
	if err != nil {
		t.Fatal(err)
	}
	if format != "png" || cfg.Width != BigW || cfg.Height != BigH {
		t.Fatalf("got %s %dx%d", format, cfg.Width, cfg.Height)
	}
	t.Logf("png: %d bytes, %dx%d = %d Mpx; DecodeConfig alocou %d bytes", len(b), cfg.Width, cfg.Height,
		cfg.Width*cfg.Height/1_000_000, after.TotalAlloc-before.TotalAlloc)
	if len(b) > 1<<20 {
		t.Errorf("arquivo grande demais para o caso (>1 MiB): %d", len(b))
	}
	if after.TotalAlloc-before.TotalAlloc > 1<<20 {
		t.Errorf("DecodeConfig alocou demais: %d", after.TotalAlloc-before.TotalAlloc)
	}
}

// caso 10: 503, 503, 200; contador por chave; /reset zera.
func TestFlaky(t *testing.T) {
	ts := httptest.NewServer(New().Handler())
	defer ts.Close()
	var got []int
	for i := 0; i < 4; i++ {
		r, _ := get(t, ts.URL+"/flaky/a")
		got = append(got, r.StatusCode)
	}
	if got[0] != 503 || got[1] != 503 || got[2] != 200 || got[3] != 200 {
		t.Fatalf("sequência %v", got)
	}
	if r, _ := get(t, ts.URL+"/flaky/b"); r.StatusCode != 503 { // outra chave começa do zero
		t.Fatalf("chave b: %d", r.StatusCode)
	}
	get(t, ts.URL+"/reset")
	if r, _ := get(t, ts.URL+"/flaky/a"); r.StatusCode != 503 {
		t.Fatalf("depois do reset: %d", r.StatusCode)
	}
	r, b := get(t, ts.URL+"/ok.jpg")
	cfg, format, err := image.DecodeConfig(bytes.NewReader(b))
	if err != nil || format != "jpeg" || cfg.Width != JPEGW || cfg.Height != JPEGH || r.Header.Get("Content-Type") != "image/jpeg" {
		t.Fatalf("jpeg: %v %s %v", err, format, cfg)
	}
}

func TestRateLimit(t *testing.T) {
	ts := httptest.NewServer(New().Handler())
	defer ts.Close()
	r, _ := get(t, ts.URL+"/ratelimit/x")
	if r.StatusCode != 429 || r.Header.Get("Retry-After") != "45" {
		t.Fatalf("1ª: %d %q", r.StatusCode, r.Header.Get("Retry-After"))
	}
	if r, _ = get(t, ts.URL+"/ratelimit/x"); r.StatusCode != 200 {
		t.Fatalf("2ª: %d", r.StatusCode)
	}
}
