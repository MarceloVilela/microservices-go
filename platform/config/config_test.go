package config_test

import (
	"strings"
	"testing"
	"time"

	"github.com/MarceloVilela/microservices-go/platform/config"
)

func vars() []config.Var {
	return []config.Var{
		{Name: "HTTP_ADDR", Default: ":8080"},
		{Name: "AMQP_URL", Required: true, Secret: true},
		{Name: "READY_TIMEOUT", Default: "2s", Validate: config.ValidDuration},
	}
}

func TestLoadAplicaPadraoEOverride(t *testing.T) {
	v, err := config.Load([]string{"AMQP_URL=amqp://u:p@h/", "HTTP_ADDR=:9"}, vars())
	if err != nil {
		t.Fatal(err)
	}
	if got := v.String("HTTP_ADDR"); got != ":9" {
		t.Errorf("HTTP_ADDR = %q", got)
	}
	if got := v.Duration("READY_TIMEOUT"); got != 2*time.Second {
		t.Errorf("READY_TIMEOUT = %v", got)
	}
}

func TestLoadAcumulaTodosOsErros(t *testing.T) {
	_, err := config.Load([]string{"READY_TIMEOUT=abc"}, vars())
	if err == nil {
		t.Fatal("esperava erro")
	}
	for _, want := range []string{"AMQP_URL", "READY_TIMEOUT"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("erro %q não cita %s", err, want)
		}
	}
}

func TestLoadDetectaNomeInvalidoQueSumiriaEmSilencio(t *testing.T) {
	_, err := config.Load([]string{"AMQP_URL=x", "http-addr=:1"}, vars())
	if err == nil || !strings.Contains(err.Error(), "HTTP_ADDR") {
		t.Fatalf("esperava erro citando HTTP_ADDR, veio %v", err)
	}
}

func TestLoadRejeitaNomeInvalidoNaDefinicao(t *testing.T) {
	_, err := config.Load(nil, []config.Var{{Name: "http-addr"}})
	if err == nil {
		t.Fatal("esperava erro de definição")
	}
}

func TestErroNaoVazaValorSecreto(t *testing.T) {
	_, err := config.Load([]string{"AMQP_URL=segredo-123", "READY_TIMEOUT=abc"}, vars())
	if err == nil || strings.Contains(err.Error(), "segredo-123") {
		t.Fatalf("erro vazou o segredo ou não existiu: %v", err)
	}
}

func TestNamesListaSoNomes(t *testing.T) {
	got := strings.Join(config.Names(vars()), ",")
	if got != "AMQP_URL,HTTP_ADDR,READY_TIMEOUT" {
		t.Errorf("Names = %s", got)
	}
}
