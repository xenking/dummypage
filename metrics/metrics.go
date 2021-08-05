package metrics

import (
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/VictoriaMetrics/metrics"
	"github.com/cornelk/hashmap"
	"github.com/gofiber/adaptor/v2"
	"github.com/gofiber/fiber/v2"
)

type Config struct {
	ServiceName string `default:"dummypage"`
	Namespace   string `default:"http"`
	Subsystem   string `default:"service"`
	DefaultURL  string `default:"/metrics"`
}

// Metrics ...
type Metrics struct {
	serviceName string
	namespace   string
	subsystem   string
	defaultURL  string
	cache *hashmap.HashMap
}

// New creates a new instance of Metrics middleware
// serviceName is available as a const label
func New(cfg Config) *Metrics {
	return &Metrics{
		serviceName: cfg.ServiceName,
		namespace:   cfg.Namespace,
		subsystem:   cfg.Subsystem,
		defaultURL:  cfg.DefaultURL,
		cache:       &hashmap.HashMap{},
	}
}

// RegisterAt will register the prometheus handler at a given URL
func (m *Metrics) RegisterAt(app *fiber.App, url ...string) {
	if len(url) > 0 {
		m.defaultURL = url[0]
	}
	app.Get(m.defaultURL, adaptor.HTTPHandlerFunc(metricsPage))
	m.initLabels()
}

func metricsPage(w http.ResponseWriter, r *http.Request) {
	metrics.WritePrometheus(w, true)
}

const (
	MetricInFlight = "requests_in_progress_total"
	MetricTotal    = "requests_total"
	MetricDuration = "request_duration_seconds"
)

const (
	LabelStatusCode = "statusCode"
	LabelMethod     = "method"
	LabelPath       = "path"
)

// Middleware is the actual default middleware implementation
func (m *Metrics) Middleware(ctx *fiber.Ctx) error {
	start := time.Now()
	method := ctx.Route().Method
	path := ctx.Route().Path

	if path == m.defaultURL {
		return ctx.Next()
	}
	counter := metrics.GetOrCreateCounter(m.makeName(MetricInFlight, method, path))
	counter.Inc()
	if err := ctx.Next(); err != nil {
		counter.Dec()
		return err
	}
	counter.Dec()
	statusCode := strconv.Itoa(ctx.Response().StatusCode())
	metrics.GetOrCreateCounter(m.makeName(MetricTotal, statusCode, method, path)).Inc()
	metrics.GetOrCreateHistogram(m.makeName(MetricDuration, statusCode, method, path)).UpdateDuration(start)
	return nil
}

func (m *Metrics) initLabels() {
	m.cache.Set(MetricInFlight, []string{LabelMethod, LabelPath})
	m.cache.Set(MetricTotal, []string{LabelStatusCode, LabelMethod, LabelPath})
	m.cache.Set(MetricDuration, []string{LabelStatusCode, LabelMethod, LabelPath})
}

func (m *Metrics) makeName(name string, values ...string) string {
	var b strings.Builder
	b.WriteString(m.namespace)
	b.WriteByte('_')
	b.WriteString(m.subsystem)
	b.WriteByte('_')
	b.WriteString(name)
	lb, exists := m.cache.Get(name)
	if !exists {
		return b.String()
	}
	labels, ok := lb.([]string)
	if !ok || len(labels) == 0 {
		return b.String()
	}
	b.WriteByte('{')
	for i, v := range values {
		if i > len(labels) {
			continue
		}
		b.WriteString(labels[i])
		b.WriteByte('=')
		b.WriteString(v)
		if i < len(labels)-1 {
			b.WriteByte(',')
		}
	}
	b.WriteByte('}')
	return b.String()
}
