package topology_test

import (
	"testing"

	"github.com/MarceloVilela/microservices-go/platform/topology"
)

func TestFilasConsistentes(t *testing.T) {
	names := map[string]bool{}
	keys := map[string]bool{}
	for _, q := range topology.Queues() {
		if names[q.Name] || keys[q.Key] {
			t.Errorf("nome ou chave repetida: %s / %s", q.Name, q.Key)
		}
		names[q.Name], keys[q.Key] = true, true
	}
	if len(topology.Queues()) != 7 {
		t.Errorf("esperava 7 filas, veio %d", len(topology.Queues()))
	}
}

func TestDegrausCrescentesComDLXDeVolta(t *testing.T) {
	var last int32
	for _, q := range topology.RetrySteps() {
		ttl := q.Args["x-message-ttl"].(int32)
		if ttl <= last {
			t.Errorf("%s: TTL %d não cresce", q.Name, ttl)
		}
		last = ttl
		if q.Args["x-dead-letter-exchange"] != topology.Exchange || q.Args["x-dead-letter-routing-key"] != topology.PhotoProcess.Key {
			t.Errorf("%s: DLX não volta para %s", q.Name, topology.PhotoProcess.Name)
		}
	}
	if len(topology.RetrySteps()) != 4 {
		t.Errorf("esperava 4 degraus")
	}
}

func TestArgsNaoCompartilhados(t *testing.T) {
	a := topology.RetrySteps()
	a[0].Args["x-message-ttl"] = int32(1)
	if topology.RetrySteps()[0].Args["x-message-ttl"].(int32) == 1 {
		t.Error("Queues devolve mapa compartilhado")
	}
}
