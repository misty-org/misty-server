package metrics

import (
	"io"
	"net/http"

	"github.com/prometheus/client_golang/prometheus"
)

func (m *Registry) initTraffic() {
	m.traffic = prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "misty_http_body_bytes_total",
		Help: "Application body bytes read/written, updated during streams; excludes headers, WebSockets, TLS and transport overhead.",
	}, []string{"route", "method", "direction"})
	m.registry.MustRegister(m.traffic)
}

func (m *Registry) recordHTTP(r *http.Request, direction string, n int) {
	if n > 0 {
		m.traffic.WithLabelValues(routePattern(r), boundedMethod(r.Method), direction).Add(float64(n))
	}
}

func boundedMethod(method string) string {
	switch method {
	case "GET", "HEAD", "POST", "PUT", "PATCH", "DELETE", "OPTIONS", "CONNECT", "TRACE":
		return method
	default:
		return "OTHER"
	}
}

type bodyCounter struct{ record func(int) }

func (c bodyCounter) Write(p []byte) (int, error) {
	c.record(len(p))
	return len(p), nil
}

type countingBody struct {
	io.ReadCloser
	record func(int)
}

func (b *countingBody) Read(p []byte) (int, error) {
	n, err := b.ReadCloser.Read(p)
	b.record(n)
	return n, err
}
