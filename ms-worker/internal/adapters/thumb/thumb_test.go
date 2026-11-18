package thumb_test

import (
	"bytes"
	"errors"
	"image"
	"image/color"
	"image/jpeg"
	"image/png"
	"testing"

	"github.com/MarceloVilela/microservices-go/ms-worker/internal/adapters/thumb"
	"github.com/MarceloVilela/microservices-go/ms-worker/internal/domain"
)

func gradient(w, h int) *image.RGBA {
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := range h {
		for x := range w {
			img.Set(x, y, color.RGBA{uint8(x * 255 / w), uint8(y * 255 / h), 128, 255})
		}
	}
	return img
}

func encodeJPEG(t *testing.T, img image.Image) []byte {
	t.Helper()
	var b bytes.Buffer
	if err := jpeg.Encode(&b, img, nil); err != nil {
		t.Fatal(err)
	}
	return b.Bytes()
}

func encodePNG(t *testing.T, img image.Image) []byte {
	t.Helper()
	var b bytes.Buffer
	if err := png.Encode(&b, img); err != nil {
		t.Fatal(err)
	}
	return b.Bytes()
}

func size(t *testing.T, jpg []byte) (int, int) {
	t.Helper()
	img, err := jpeg.Decode(bytes.NewReader(jpg))
	if err != nil {
		t.Fatalf("a miniatura não é um JPEG válido: %v", err)
	}
	return img.Bounds().Dx(), img.Bounds().Dy()
}

func TestDimensoesDoDataset(t *testing.T) {
	m := thumb.Maker{MaxPixels: 25_000_000}
	for name, src := range map[string][]byte{
		"jpeg 1200x800": encodeJPEG(t, gradient(1200, 800)),
		"png 600x400":   encodePNG(t, gradient(600, 400)),
	} {
		out, err := m.Thumbnail(src)
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if w, h := size(t, out); w != 200 || h != 133 {
			t.Errorf("%s: %dx%d, esperado 200x133", name, w, h)
		}
	}
}

func TestAlturaArredondada(t *testing.T) {
	for _, c := range []struct{ w, h, want int }{{1200, 800, 133}, {600, 400, 133}, {200, 200, 200}, {1000, 1, 1}, {100, 300, 600}} {
		if got := thumb.Height(c.w, c.h); got != c.want {
			t.Errorf("Height(%d,%d) = %d, esperado %d", c.w, c.h, got, c.want)
		}
	}
}

func TestDeterministica(t *testing.T) {
	m := thumb.Maker{MaxPixels: 1_000_000}
	src := encodeJPEG(t, gradient(640, 480))
	a, err1 := m.Thumbnail(src)
	b, err2 := m.Thumbnail(src)
	if err1 != nil || err2 != nil || !bytes.Equal(a, b) {
		t.Fatalf("mesma entrada deve dar os mesmos bytes (%v, %v)", err1, err2)
	}
}

func TestAlfaViraBranco(t *testing.T) {
	img := image.NewNRGBA(image.Rect(0, 0, 400, 400)) // totalmente transparente
	out, err := thumb.Maker{MaxPixels: 1_000_000}.Thumbnail(encodePNG(t, img))
	if err != nil {
		t.Fatal(err)
	}
	dec, _ := jpeg.Decode(bytes.NewReader(out))
	if r, g, b, _ := dec.At(100, 100).RGBA(); r>>8 < 250 || g>>8 < 250 || b>>8 < 250 {
		t.Errorf("pixel transparente deveria ficar branco: %d %d %d", r>>8, g>>8, b>>8)
	}
}

func permanent(t *testing.T, err error, reason string) {
	t.Helper()
	var f *domain.Failure
	if !errors.As(err, &f) || f.Class != domain.Permanent || f.Reason != reason {
		t.Errorf("erro = %v, esperado falha definitiva %q", err, reason)
	}
}

func TestEntradaInvalida(t *testing.T) {
	m := thumb.Maker{MaxPixels: 1_000_000}
	_, err := m.Thumbnail([]byte("isto não é uma imagem"))
	permanent(t, err, "imagem inválida")
	_, err = m.Thumbnail(nil)
	permanent(t, err, "imagem inválida")
	// cabeçalho válido, corpo truncado: DecodeConfig passa, Decode falha
	full := encodeJPEG(t, gradient(300, 300))
	_, err = m.Thumbnail(full[:len(full)/3])
	permanent(t, err, "imagem inválida")
}

func TestTetoDePixelsAntesDeDecodificar(t *testing.T) {
	src := encodePNG(t, gradient(100, 100)) // 10.000 px
	_, err := thumb.Maker{MaxPixels: 9_999}.Thumbnail(src)
	permanent(t, err, "pixels acima do teto")
	if _, err := (thumb.Maker{MaxPixels: 10_000}).Thumbnail(src); err != nil {
		t.Errorf("no teto exato deve passar: %v", err)
	}
}

func TestProporcaoExtremaEhRecusadaAntesDeAlocar(t *testing.T) {
	// 1x4000 (4.000 px, bem abaixo do teto): a miniatura teria 800.000 linhas
	src := encodePNG(t, image.NewGray(image.Rect(0, 0, 1, 4000)))
	_, err := thumb.Maker{MaxPixels: 25_000_000}.Thumbnail(src)
	permanent(t, err, "proporção da imagem extrema")
	// 200x10000 (altura destino 10000 > 2000) também; 200x2000 (altura destino 2000) passa
	_, err = thumb.Maker{MaxPixels: 25_000_000}.Thumbnail(encodePNG(t, image.NewGray(image.Rect(0, 0, 200, 10000))))
	permanent(t, err, "proporção da imagem extrema")
	if _, err := (thumb.Maker{MaxPixels: 25_000_000}).Thumbnail(encodePNG(t, image.NewGray(image.Rect(0, 0, 200, 2000)))); err != nil {
		t.Errorf("no limite deve passar: %v", err)
	}
}
