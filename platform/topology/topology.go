// Package topology define os nomes, chaves e argumentos do broker.
//
// Só define. Quem declara (cria) é exclusivamente o comando topology-init de
// ms-queues (ADR 0015); os serviços apenas verificam com declaração passiva.
package topology

import "maps"

// Exchange é a exchange direct durável por onde passa tudo.
const Exchange = "photos"

// Queue descreve uma fila durável ligada à exchange Exchange.
type Queue struct {
	Name string
	Key  string
	Args map[string]any
}

// Cópia com Args próprio, para ninguém alterar a definição compartilhada.
func (q Queue) clone() Queue {
	q.Args = maps.Clone(q.Args)
	return q
}

var (
	// PhotoProcess recebe os jobs de processamento.
	PhotoProcess = Queue{Name: "photo.process", Key: "process"}
	// PhotoDLQ guarda o que falhou de vez ou esgotou os degraus.
	PhotoDLQ = Queue{Name: "photo.process.dlq", Key: "dlq"}
	// BatchCompleted recebe o evento de fim de lote.
	BatchCompleted = Queue{Name: "batch.completed", Key: "batch.completed"}
)

func step(suffix string, ttlMillis int32) Queue {
	return Queue{
		Name: "photo.process.retry." + suffix,
		Key:  "retry." + suffix,
		Args: map[string]any{
			"x-message-ttl":             ttlMillis,
			"x-dead-letter-exchange":    Exchange,
			"x-dead-letter-routing-key": PhotoProcess.Key,
		},
	}
}

// RetrySteps devolve a escada de retry (ADR 0005): TTL fixo por fila e DLX de
// volta para photo.process.
func RetrySteps() []Queue {
	return []Queue{
		step("5s", 5_000),
		step("30s", 30_000),
		step("2m", 120_000),
		step("5m", 300_000),
	}
}

// Queues devolve todas as filas, na ordem de declaração.
func Queues() []Queue {
	qs := []Queue{PhotoProcess.clone()}
	for _, s := range RetrySteps() {
		qs = append(qs, s.clone())
	}
	return append(qs, PhotoDLQ.clone(), BatchCompleted.clone())
}
