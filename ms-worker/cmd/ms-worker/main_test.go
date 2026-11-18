package main

import (
	"testing"
	"time"

	"github.com/MarceloVilela/microservices-go/platform/service"
)

// CONSUMER_BACKOFF_MAX precisa estar declarada: o config só guarda variáveis declaradas, e sem ela
// o backoff do Supervisor valeria 0 (laço quente com o broker fora).
func TestVariavelDeBackoffDeclarada(t *testing.T) {
	found := false
	for _, v := range service.Vars(spec()) {
		found = found || v.Name == "CONSUMER_BACKOFF_MAX"
	}
	if !found {
		t.Fatal("CONSUMER_BACKOFF_MAX não declarada em spec()")
	}
}

func TestStepTTLsVemDaTopologia(t *testing.T) {
	got := stepTTLs()
	if len(got) != 4 || got[0] != 5*time.Second || got[3] != 5*time.Minute {
		t.Errorf("stepTTLs = %v", got)
	}
}
