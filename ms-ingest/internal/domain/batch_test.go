package domain_test

import (
	"errors"
	"strings"
	"testing"

	"github.com/MarceloVilela/microservices-go/ms-ingest/internal/domain"
)

var allowed = []string{"picsum.photos", "fastly.picsum.photos"}

func req(urls ...string) domain.Request {
	var r domain.Request
	for _, u := range urls {
		r.Photos = append(r.Photos, domain.PhotoRequest{SourceURL: u})
	}
	return r
}

func TestValidateAceita(t *testing.T) {
	ok := []string{
		"https://picsum.photos/id/10/1200/800",
		"https://FASTLY.picsum.photos/id/10/1200/800",
		"https://picsum.photos:443/id/10/1200/800",
	}
	for _, u := range ok {
		if err := req(u).Validate(allowed); err != nil {
			t.Errorf("%s: %v", u, err)
		}
	}
}

func TestValidateRecusa(t *testing.T) {
	cases := map[string]domain.Request{
		"lista vazia":      req(),
		"101 fotos":        req(make([]string, 101)...),
		"host fora":        req("https://example.com/foto.jpg"),
		"http":             req("http://picsum.photos/id/1/1/1"),
		"credencial":       req("https://user:pw@picsum.photos/id/1/1/1"),
		"porta":            req("https://picsum.photos:8443/id/1/1/1"),
		"loopback":         req("https://127.0.0.1/x"),
		"sufixo enganoso":  req("https://picsum.photos.evil.com/x"),
		"prefixo enganoso": req("https://evilpicsum.photos/x"),
		"vazia":            req(""),
		"sem host":         req("https:///x"),
		"não é URL":        req("::::"),
		"muito longa":      req("https://picsum.photos/" + strings.Repeat("a", 2048)),
		"esquema file":     req("file:///etc/passwd"),
	}
	for name, r := range cases {
		err := r.Validate(allowed)
		if err == nil {
			t.Errorf("%s: esperava erro", name)
			continue
		}
		var ve *domain.ValidationError
		if !errors.As(err, &ve) {
			t.Errorf("%s: erro não é ValidationError: %v", name, err)
		}
	}
}

func TestValidateAceita100(t *testing.T) {
	urls := make([]string, 100)
	for i := range urls {
		urls[i] = "https://picsum.photos/id/1/1/1"
	}
	if err := req(urls...).Validate(allowed); err != nil {
		t.Error(err)
	}
}

func TestValidKey(t *testing.T) {
	for _, k := range []string{"a", "abc-123_x.y", strings.Repeat("k", 128)} {
		if !domain.ValidKey(k) {
			t.Errorf("%q deveria valer", k)
		}
	}
	for _, k := range []string{"", strings.Repeat("k", 129), "com espaço", "ação", "a/b", "a\nb"} {
		if domain.ValidKey(k) {
			t.Errorf("%q não deveria valer", k)
		}
	}
}

func TestHashEstavelEDistinto(t *testing.T) {
	a, b := req("https://picsum.photos/id/1/1/1"), req("https://picsum.photos/id/1/1/1")
	if a.Hash() != b.Hash() {
		t.Error("mesmo corpo, hash diferente")
	}
	if a.Hash() == req("https://picsum.photos/id/2/1/1").Hash() {
		t.Error("corpos diferentes, mesmo hash")
	}
	// a ordem importa (é outro lote); e a junção não pode colidir
	if req("https://picsum.photos/a", "https://picsum.photos/b").Hash() == req("https://picsum.photos/a\nhttps://picsum.photos/b").Hash() {
		t.Error("colisão por junção")
	}
}

func TestNewID(t *testing.T) {
	a, b := domain.NewID(), domain.NewID()
	if a == b || len(a) != 36 || a[14] != '4' {
		t.Errorf("ids: %s %s", a, b)
	}
}
