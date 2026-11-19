// Package metrics reúne o que os serviços expõem em /metrics (ADR 0009): um
// registro Prometheus por processo (não o global), o middleware HTTP com a rota
// como rótulo e um cache para consultas caras feitas a cada scrape.
package metrics

import (
	"net/http"
	"strconv"
	"sync"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/collectors"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

// Registry é o registro de um serviço, já com as métricas do runtime Go e do processo
// e as do HTTP.
type Registry struct {
	reg      *prometheus.Registry
	requests *prometheus.CounterVec
	latency  *prometheus.HistogramVec
}

// NewRegistry cria um registro novo.
func NewRegistry() *Registry {
	r := &Registry{
		reg: prometheus.NewRegistry(),
		requests: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "http_requests_total", Help: "Requisições HTTP por padrão de rota e status.",
		}, []string{"route", "code"}),
		latency: prometheus.NewHistogramVec(prometheus.HistogramOpts{
			Name: "http_request_seconds", Help: "Latência das requisições HTTP por padrão de rota.",
			Buckets: []float64{.005, .01, .025, .05, .1, .25, .5, 1, 2.5, 5, 10},
		}, []string{"route"}),
	}
	r.reg.MustRegister(collectors.NewGoCollector(), collectors.NewProcessCollector(collectors.ProcessCollectorOpts{}), r.requests, r.latency)
	return r
}

// Handler serve o formato de texto do Prometheus.
func (r *Registry) Handler() http.Handler {
	return promhttp.HandlerFor(r.reg, promhttp.HandlerOpts{})
}

// MustRegister registra coletores do serviço.
func (r *Registry) MustRegister(c ...prometheus.Collector) { r.reg.MustRegister(c...) }

// Counter cria e registra um contador simples.
func (r *Registry) Counter(name, help string) prometheus.Counter {
	c := prometheus.NewCounter(prometheus.CounterOpts{Name: name, Help: help})
	r.reg.MustRegister(c)
	return c
}

// CounterVec cria e registra um contador com rótulos.
func (r *Registry) CounterVec(name, help string, labels ...string) *prometheus.CounterVec {
	c := prometheus.NewCounterVec(prometheus.CounterOpts{Name: name, Help: help}, labels)
	r.reg.MustRegister(c)
	return c
}

// HistogramVec cria e registra um histograma com rótulos.
func (r *Registry) HistogramVec(name, help string, buckets []float64, labels ...string) *prometheus.HistogramVec {
	h := prometheus.NewHistogramVec(prometheus.HistogramOpts{Name: name, Help: help, Buckets: buckets}, labels)
	r.reg.MustRegister(h)
	return h
}

// Middleware conta e mede cada requisição. O rótulo "route" é o padrão do ServeMux
// ("GET /photos/{id}/thumb"), nunca o caminho cru: caminho livre explode a cardinalidade.
// Sem padrão (404 do mux) vira "unmatched".
func (r *Registry) Middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		start := time.Now()
		rec := &statusWriter{ResponseWriter: w, code: http.StatusOK}
		next.ServeHTTP(rec, req)
		route := req.Pattern // o ServeMux preenche no próprio *Request
		if route == "" {
			route = "unmatched"
		}
		r.requests.WithLabelValues(route, strconv.Itoa(rec.code)).Inc()
		r.latency.WithLabelValues(route).Observe(time.Since(start).Seconds())
	})
}

type statusWriter struct {
	http.ResponseWriter
	code        int
	wroteHeader bool
}

func (s *statusWriter) WriteHeader(code int) {
	if !s.wroteHeader {
		s.code, s.wroteHeader = code, true
	}
	s.ResponseWriter.WriteHeader(code)
}

func (s *statusWriter) Write(b []byte) (int, error) {
	s.wroteHeader = true
	return s.ResponseWriter.Write(b)
}

// Flush mantém o SSE funcionando através do wrapper.
func (s *statusWriter) Flush() {
	s.wroteHeader = true
	if f, ok := s.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}

// Unwrap deixa o http.ResponseController alcançar o writer original (deadline de escrita do SSE).
func (s *statusWriter) Unwrap() http.ResponseWriter { return s.ResponseWriter }

// Cache guarda o resultado de uma coleta cara (consulta ao Postgres ou ao broker)
// por ttl, para que vários scrapes seguidos não repitam a consulta.
type Cache struct {
	mu      sync.Mutex
	collect func() (map[string]float64, error)
	ttl     time.Duration
	at      time.Time
	last    map[string]float64
	lastErr error
}

// NewCache cria o cache. Se a coleta falha, o erro também vale pelo ttl (a métrica some do scrape,
// e um banco fora não faz cada scrape repetir a consulta).
func NewCache(collect func() (map[string]float64, error), ttl time.Duration) *Cache {
	return &Cache{collect: collect, ttl: ttl}
}

// Get devolve o último resultado se ainda estiver dentro do ttl, senão coleta de novo.
func (c *Cache) Get() (map[string]float64, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if !c.at.IsZero() && time.Since(c.at) < c.ttl {
		return c.last, c.lastErr
	}
	c.last, c.lastErr = c.collect()
	c.at = time.Now()
	return c.last, c.lastErr
}

// GaugeFunc registra um coletor que expõe, a cada scrape, um gauge por chave do mapa
// devolvido pelo cache (rótulo único). Falha da coleta omite a métrica.
func (r *Registry) GaugeFunc(name, help, label string, c *Cache) {
	r.reg.MustRegister(&gaugeMap{desc: prometheus.NewDesc(name, help, []string{label}, nil), cache: c})
}

type gaugeMap struct {
	desc  *prometheus.Desc
	cache *Cache
}

func (g *gaugeMap) Describe(ch chan<- *prometheus.Desc) { ch <- g.desc }

func (g *gaugeMap) Collect(ch chan<- prometheus.Metric) {
	vals, err := g.cache.Get()
	if err != nil {
		return
	}
	for k, v := range vals {
		ch <- prometheus.MustNewConstMetric(g.desc, prometheus.GaugeValue, v, k)
	}
}

// Gauge registra um gauge sem rótulos lido a cada scrape; erro de f omite a métrica.
func (r *Registry) Gauge(name, help string, f func() (float64, error)) {
	r.reg.MustRegister(&gaugeOne{desc: prometheus.NewDesc(name, help, nil, nil), f: f})
}

type gaugeOne struct {
	desc *prometheus.Desc
	f    func() (float64, error)
}

func (g *gaugeOne) Describe(ch chan<- *prometheus.Desc) { ch <- g.desc }

func (g *gaugeOne) Collect(ch chan<- prometheus.Metric) {
	if v, err := g.f(); err == nil {
		ch <- prometheus.MustNewConstMetric(g.desc, prometheus.GaugeValue, v)
	}
}

// Outcomes é um contador com um único rótulo "outcome" (desfecho de uma mensagem).
type Outcomes struct{ v *prometheus.CounterVec }

// Inc conta um desfecho.
func (o Outcomes) Inc(outcome string) { o.v.WithLabelValues(outcome).Inc() }

// Outcomes cria e registra o contador name{outcome}. Evita que os serviços importem o client Prometheus.
func (r *Registry) Outcomes(name, help string) Outcomes {
	return Outcomes{r.CounterVec(name, help, "outcome")}
}
