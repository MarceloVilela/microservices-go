package main

import (
	"log/slog"
	"testing"

	"github.com/MarceloVilela/microservices-go/platform/config"
	"github.com/MarceloVilela/microservices-go/platform/service"
)

func vals(t *testing.T, env ...string) config.Values {
	t.Helper()
	v, err := config.Load(append([]string{"AMQP_URL=amqp://u:p@h:5672/", "DATABASE_URL=postgres://u:p@h/db"}, env...), service.Vars(spec()))
	if err != nil {
		t.Fatal(err)
	}
	return v
}

func TestSemSMTPSoOSlogNotifica(t *testing.T) {
	ns, err := notifiers(vals(t), slog.New(slog.DiscardHandler))
	if err != nil || len(ns) != 1 || ns[0].Name() != "slog" {
		t.Fatalf("ns=%v err=%v", ns, err)
	}
}

func TestSMTPExigeRemetenteEDestinatario(t *testing.T) {
	if _, err := notifiers(vals(t, "SMTP_ADDR=mailpit:1025", "SMTP_FROM=a@b"), slog.New(slog.DiscardHandler)); err == nil {
		t.Fatal("esperava erro sem SMTP_TO")
	}
	ns, err := notifiers(vals(t, "SMTP_ADDR=mailpit:1025", "SMTP_FROM=a@b", "SMTP_TO=c@d"), slog.New(slog.DiscardHandler))
	if err != nil || len(ns) != 2 {
		t.Fatalf("ns=%v err=%v", ns, err)
	}
}

func TestVariaveisDeBackoffEShutdownDeclaradas(t *testing.T) {
	want := map[string]bool{"CONSUMER_BACKOFF_MAX": false, "SHUTDOWN_TIMEOUT": false}
	for _, v := range service.Vars(spec()) {
		if _, ok := want[v.Name]; ok {
			want[v.Name] = true
		}
	}
	for n, ok := range want {
		if !ok {
			t.Errorf("%s não declarada", n)
		}
	}
}
