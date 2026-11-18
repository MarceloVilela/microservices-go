package app_test

import (
	"testing"
	"time"

	"github.com/MarceloVilela/microservices-go/ms-api/internal/app"
)

func TestCursorIdaEVolta(t *testing.T) {
	want := app.Cursor{T: time.Date(2026, 11, 18, 10, 30, 0, 123456000, time.UTC), ID: "0192f0c0-1111-7222-8333-444455556666"}
	got, err := app.DecodeCursor(want.Encode())
	if err != nil {
		t.Fatal(err)
	}
	if !got.T.Equal(want.T) || got.ID != want.ID {
		t.Errorf("got %+v, quer %+v", got, want)
	}
}

func TestCursorAdulteradoOuInvalido(t *testing.T) {
	for _, in := range []string{
		"não é base64!", "", "YWJj", // "abc": sem separador
		app.Cursor{T: time.Now(), ID: "x"}.Encode(),                // id que não é UUID
		app.Cursor{T: time.Now(), ID: "../../etc/passwd"}.Encode(), // lixo no id
	} {
		if _, err := app.DecodeCursor(in); err == nil {
			t.Errorf("DecodeCursor(%q) deveria falhar", in)
		}
	}
}

func TestValidUUID(t *testing.T) {
	if !app.ValidUUID("0192f0c0-1111-7222-8333-444455556666") {
		t.Error("UUID válido recusado")
	}
	for _, s := range []string{"", "abc", "0192F0C0-1111-7222-8333-444455556666 ", "../x", "0192f0c0-1111-7222-8333-44445555666z"} {
		if app.ValidUUID(s) {
			t.Errorf("ValidUUID(%q) deveria ser falso", s)
		}
	}
}
