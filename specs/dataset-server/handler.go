// Servidor local de teste do dataset de paridade: casos 9 (teto de pixels) e 10 (falha transitória),
// mais um 429 com Retry-After para a Fase 4. Só para testes; nunca expor fora do loopback.
package main

import (
	"bytes"
	"compress/zlib"
	"encoding/binary"
	"fmt"
	"hash/crc32"
	"image"
	"image/color"
	"image/jpeg"
	"net/http"
	"strings"
	"sync"
)

const (
	BigW, BigH = 30000, 30000 // 900 megapixels declarados
	JPEGW      = 600
	JPEGH      = 400
	FlakyFails = 2 // primeiras tentativas com 503
)

type Server struct {
	mu       sync.Mutex
	hits     map[string]int
	bigPNG   []byte
	smallJPG []byte
}

func New() *Server {
	return &Server{hits: map[string]int{}, bigPNG: makeBigPNG(BigW, BigH), smallJPG: makeJPEG(JPEGW, JPEGH)}
}

func chunk(buf *bytes.Buffer, typ string, data []byte) {
	binary.Write(buf, binary.BigEndian, uint32(len(data)))
	buf.WriteString(typ)
	buf.Write(data)
	c := crc32.NewIEEE()
	c.Write([]byte(typ))
	c.Write(data)
	binary.Write(buf, binary.BigEndian, c.Sum32())
}

// PNG válido de 1 bit em tons de cinza, w x h, todo preto: poucas centenas de KB e dimensões enormes.
func makeBigPNG(w, h int) []byte {
	var out bytes.Buffer
	out.WriteString("\x89PNG\r\n\x1a\n")
	ihdr := make([]byte, 13)
	binary.BigEndian.PutUint32(ihdr[0:], uint32(w))
	binary.BigEndian.PutUint32(ihdr[4:], uint32(h))
	ihdr[8], ihdr[9] = 1, 0 // profundidade 1 bit, cinza
	chunk(&out, "IHDR", ihdr)
	var z bytes.Buffer
	zw, _ := zlib.NewWriterLevel(&z, zlib.BestSpeed)
	row := make([]byte, 1+(w+7)/8) // filtro 0 + linha zerada
	for i := 0; i < h; i++ {
		zw.Write(row)
	}
	zw.Close()
	chunk(&out, "IDAT", z.Bytes())
	chunk(&out, "IEND", nil)
	return out.Bytes()
}

func makeJPEG(w, h int) []byte {
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			img.Set(x, y, color.RGBA{uint8(x * 255 / w), uint8(y * 255 / h), 128, 255})
		}
	}
	var b bytes.Buffer
	jpeg.Encode(&b, img, &jpeg.Options{Quality: 80})
	return b.Bytes()
}

func (s *Server) hit(key string) int {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.hits[key]++
	return s.hits[key]
}

func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/ok.jpg", func(w http.ResponseWriter, _ *http.Request) { s.jpeg(w) })
	// caso 9: PNG pequeno em bytes, enorme em pixels
	mux.HandleFunc("/big-pixels.png", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "image/png")
		w.Write(s.bigPNG)
	})
	// caso 10: 503 nas duas primeiras tentativas de cada chave, depois 200
	mux.HandleFunc("/flaky/", func(w http.ResponseWriter, r *http.Request) {
		key := strings.TrimPrefix(r.URL.Path, "/flaky/")
		if n := s.hit("flaky:" + key); n <= FlakyFails {
			http.Error(w, fmt.Sprintf("indisponível (tentativa %d)", n), http.StatusServiceUnavailable)
			return
		}
		s.jpeg(w)
	})
	// Fase 4: 429 com Retry-After: 45 na primeira tentativa de cada chave, depois 200
	mux.HandleFunc("/ratelimit/", func(w http.ResponseWriter, r *http.Request) {
		key := strings.TrimPrefix(r.URL.Path, "/ratelimit/")
		if s.hit("rl:"+key) == 1 {
			w.Header().Set("Retry-After", "45")
			http.Error(w, "muitas requisições", http.StatusTooManyRequests)
			return
		}
		s.jpeg(w)
	})
	mux.HandleFunc("/hits/", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprintf(w, "%d", s.hit2(strings.TrimPrefix(r.URL.Path, "/hits/")))
	})
	mux.HandleFunc("/reset", func(w http.ResponseWriter, _ *http.Request) {
		s.mu.Lock()
		s.hits = map[string]int{}
		s.mu.Unlock()
		w.Write([]byte("ok"))
	})
	return mux
}

func (s *Server) hit2(k string) int { s.mu.Lock(); defer s.mu.Unlock(); return s.hits[k] }

func (s *Server) jpeg(w http.ResponseWriter) {
	w.Header().Set("Content-Type", "image/jpeg")
	w.Write(s.smallJPG)
}
