package app_test

import (
	"context"
	"errors"
	"testing"

	"github.com/MarceloVilela/microservices-go/ms-ingest/internal/app"
	"github.com/MarceloVilela/microservices-go/ms-ingest/internal/domain"
)

type fakeStore struct {
	calls int
	got   app.NewBatch
	err   error
}

func (f *fakeStore) CreateBatch(_ context.Context, b app.NewBatch) (app.Stored, error) {
	f.calls++
	f.got = b
	return app.Stored{ID: b.ID, Total: len(b.Photos)}, f.err
}

func svc(f *fakeStore) app.Service {
	return app.Service{Store: f, Allowed: []string{"picsum.photos"}}
}

func TestEntradaInvalidaNaoTocaNoStore(t *testing.T) {
	f := &fakeStore{}
	_, err := svc(f).CreateBatch(context.Background(), "", domain.Request{Photos: []domain.PhotoRequest{{SourceURL: "https://example.com/x"}}})
	var ve *domain.ValidationError
	if !errors.As(err, &ve) || f.calls != 0 {
		t.Errorf("err=%v calls=%d", err, f.calls)
	}
	_, err = svc(f).CreateBatch(context.Background(), "chave inválida", domain.Request{Photos: []domain.PhotoRequest{{SourceURL: "https://picsum.photos/id/1/1/1"}}})
	if !errors.As(err, &ve) || f.calls != 0 {
		t.Errorf("chave: err=%v calls=%d", err, f.calls)
	}
}

func TestCriaLoteComUmaFotoPorURL(t *testing.T) {
	f := &fakeStore{}
	r := domain.Request{Photos: []domain.PhotoRequest{{SourceURL: "https://picsum.photos/id/1/1/1"}, {SourceURL: "https://picsum.photos/id/2/1/1"}}}
	got, err := svc(f).CreateBatch(context.Background(), "k1", r)
	if err != nil || got.Total != 2 {
		t.Fatalf("got=%+v err=%v", got, err)
	}
	if f.got.IdempotencyKey != "k1" || f.got.RequestHash != r.Hash() || f.got.Photos[0].ID == f.got.Photos[1].ID {
		t.Errorf("batch = %+v", f.got)
	}
}
