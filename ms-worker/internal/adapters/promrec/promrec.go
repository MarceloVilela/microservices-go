// Package promrec liga o Processor às métricas Prometheus (app.Recorder) e expõe a
// profundidade das filas (F7-2).
package promrec

import (
	"context"
	"time"

	"github.com/prometheus/client_golang/prometheus"

	"github.com/MarceloVilela/microservices-go/platform/amqpx"
	"github.com/MarceloVilela/microservices-go/platform/metrics"
	"github.com/MarceloVilela/microservices-go/platform/topology"
)

// Recorder implementa app.Recorder.
type Recorder struct {
	processed *prometheus.CounterVec
	seconds   *prometheus.HistogramVec
}

// New registra photos_processed_total e photo_process_seconds, e queue_messages (consulta ao
// broker no máximo a cada ttl; broker fora ⇒ a métrica some do scrape, o /metrics segue 200).
func New(reg *metrics.Registry, amqpURL string, queues []topology.Queue, ttl time.Duration) *Recorder {
	r := &Recorder{
		processed: reg.CounterVec("photos_processed_total", "Mensagens photo.process tratadas, por desfecho.", "outcome"),
		seconds: reg.HistogramVec("photo_process_seconds", "Duração do tratamento de uma mensagem (baixar, miniatura, transação), por desfecho.",
			[]float64{.01, .05, .1, .25, .5, 1, 2.5, 5, 10, 30}, "outcome"),
	}
	reg.GaugeFunc("queue_messages", "Mensagens prontas por fila (declaração passiva, cache curto).", "queue",
		metrics.NewCache(func() (map[string]float64, error) {
			ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			defer cancel()
			return amqpx.Depths(ctx, amqpURL, queues)
		}, ttl))
	return r
}

// Processed implementa app.Recorder.
func (r *Recorder) Processed(outcome string, d time.Duration) {
	r.processed.WithLabelValues(outcome).Inc()
	r.seconds.WithLabelValues(outcome).Observe(d.Seconds())
}
