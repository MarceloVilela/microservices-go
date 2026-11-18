// Package thumb gera a miniatura: JPEG de 200 px de largura e altura proporcional
// arredondada (specs/dataset-de-paridade.md, ADR 0017). Só stdlib; o redimensionamento é uma
// média por área em aritmética inteira, então a mesma entrada sempre dá os mesmos bytes.
package thumb

import (
	"bytes"
	"errors"
	"fmt"
	"image"
	"image/jpeg"
	_ "image/png" // registra o decoder PNG
	"io"

	"github.com/MarceloVilela/microservices-go/ms-worker/internal/domain"
)

// Width é a largura da miniatura.
const Width = 200

// MaxHeight é a altura máxima da miniatura. MaxPixels limita largura × altura, não a proporção:
// uma imagem de 1×25.000.000 passaria e pediria bilhões de linhas de destino.
const MaxHeight = 2000

// Quality é a qualidade JPEG fixa.
const Quality = 85

// Maker implementa app.Thumbnailer.
type Maker struct {
	// MaxPixels é o teto de largura × altura aceito (checado por DecodeConfig, antes de decodificar).
	MaxPixels int
}

// Thumbnail decodifica src (JPEG ou PNG) e devolve a miniatura em JPEG.
func (m Maker) Thumbnail(src []byte) ([]byte, error) {
	// DecodeConfig lê só o cabeçalho: recusa bomba de descompressão sem alocar a imagem
	cfg, format, err := image.DecodeConfig(bytes.NewReader(src))
	if err != nil {
		return nil, domain.NewPermanent("imagem inválida", err)
	}
	if format != "jpeg" && format != "png" {
		return nil, domain.NewPermanent("formato de imagem não suportado", nil)
	}
	if cfg.Width <= 0 || cfg.Height <= 0 {
		return nil, domain.NewPermanent("imagem inválida", errors.New("dimensão zero"))
	}
	if int64(cfg.Width)*int64(cfg.Height) > int64(m.MaxPixels) {
		return nil, domain.NewPermanent("pixels acima do teto", fmt.Errorf("%dx%d", cfg.Width, cfg.Height))
	}
	if Height(cfg.Width, cfg.Height) > MaxHeight {
		return nil, domain.NewPermanent("proporção da imagem extrema", fmt.Errorf("%dx%d", cfg.Width, cfg.Height))
	}
	img, _, err := image.Decode(bytes.NewReader(src))
	if err != nil {
		return nil, domain.NewPermanent("imagem inválida", err)
	}
	var out bytes.Buffer
	if err := jpeg.Encode(io.Writer(&out), resize(img), &jpeg.Options{Quality: Quality}); err != nil {
		return nil, fmt.Errorf("codificar miniatura: %w", err)
	}
	return out.Bytes(), nil
}

// Height é a altura da miniatura de uma imagem w×h: round(h × 200 / w), no mínimo 1.
func Height(w, h int) int {
	return max(1, (h*Width+w/2)/w)
}

// resize reduz (ou amplia) para Width × Height(w,h) pela média da área de origem de cada
// pixel de destino. Pixels com alfa são compostos sobre branco (JPEG não tem alfa).
func resize(src image.Image) *image.RGBA {
	b := src.Bounds()
	sw, sh := b.Dx(), b.Dy()
	dw, dh := Width, Height(sw, sh)
	dst := image.NewRGBA(image.Rect(0, 0, dw, dh))
	for y := range dh {
		y0, y1 := span(y, sh, dh)
		for x := range dw {
			x0, x1 := span(x, sw, dw)
			var r, g, bl, a, n uint64
			for sy := y0; sy < y1; sy++ {
				for sx := x0; sx < x1; sx++ {
					pr, pg, pb, pa := src.At(b.Min.X+sx, b.Min.Y+sy).RGBA()
					r, g, bl, a, n = r+uint64(pr), g+uint64(pg), bl+uint64(pb), a+uint64(pa), n+1
				}
			}
			// valores de 16 bits pré-multiplicados; o que falta de alfa vira branco
			white := n*0xffff - a
			i := dst.PixOffset(x, y)
			dst.Pix[i+0] = to8((r + white) / n)
			dst.Pix[i+1] = to8((g + white) / n)
			dst.Pix[i+2] = to8((bl + white) / n)
			dst.Pix[i+3] = 0xff
		}
	}
	return dst
}

// span devolve o intervalo [lo, hi) de origem (de 0 a srcN) que corresponde ao pixel i de dstN.
func span(i, srcN, dstN int) (lo, hi int) {
	lo = i * srcN / dstN
	hi = ((i+1)*srcN + dstN - 1) / dstN
	if hi <= lo {
		hi = lo + 1
	}
	return lo, min(hi, srcN)
}

// to8 converte um valor de 16 bits (média de valores de 16 bits, no máximo 0xffff) para 8 bits.
func to8(v uint64) uint8 {
	return uint8(min(v>>8, 0xff)) //nolint:gosec // limitado a 0xff na linha acima
}
