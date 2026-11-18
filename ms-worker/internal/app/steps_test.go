package app_test

import (
	"testing"
	"time"

	"github.com/MarceloVilela/microservices-go/ms-worker/internal/app"
)

func TestStepFor(t *testing.T) {
	ttls := []time.Duration{5 * time.Second, 30 * time.Second, 2 * time.Minute, 5 * time.Minute}
	for _, c := range []struct {
		name    string
		attempt int
		ra      time.Duration
		want    int
	}{
		{"sem Retry-After usa x-attempt", 1, 0, 1},
		{"5 s cai no primeiro degrau", 0, 5 * time.Second, 0},
		{"arredonda para cima", 0, 6 * time.Second, 1},
		{"45 s vai para 2m", 0, 45 * time.Second, 2},
		{"nunca abaixo de x-attempt", 3, 5 * time.Second, 3},
		{"acima de 5 min usa o último", 0, 301 * time.Second, 3},
		{"negativo ignora", 2, -time.Second, 2},
		{"attempt fora da escada é limitado", 9, 0, 3},
	} {
		if got := app.StepFor(c.attempt, c.ra, ttls); got != c.want {
			t.Errorf("%s: degrau = %d, quer %d", c.name, got, c.want)
		}
	}
}
