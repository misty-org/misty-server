package metrics

import "github.com/prometheus/client_golang/prometheus"

func (m *Registry) initAIStreams() {
	m.aiStreamReads = prometheus.NewCounterVec(prometheus.CounterOpts{Name: "misty_ai_stream_reads_total", Help: "Durable event-page reads by live hub or replay viewer and outcome."}, []string{"source", "result"})
	m.aiStreamEvents = prometheus.NewCounterVec(prometheus.CounterOpts{Name: "misty_ai_stream_events_read_total", Help: "Events decoded from durable live or replay pages."}, []string{"source"})
	m.aiStreamBytes = prometheus.NewCounterVec(prometheus.CounterOpts{Name: "misty_ai_stream_payload_bytes_read_total", Help: "Serialized event payload bytes in durable pages, excluding SQL/transport overhead."}, []string{"source"})
	m.aiStreamWindows = prometheus.NewGauge(prometheus.GaugeOpts{Name: "misty_ai_stream_windows", Help: "Shared invocation stream windows with at least one attached reader."})
	m.registry.MustRegister(m.aiStreamReads, m.aiStreamEvents, m.aiStreamBytes, m.aiStreamWindows)
}
func (m *Registry) RecordAIStreamRead(source string, events, bytes int, success bool) {
	if m == nil || (source != "live" && source != "replay") {
		return
	}
	result := "error"
	if success {
		result = "ok"
	}
	m.aiStreamReads.WithLabelValues(source, result).Inc()
	if success {
		m.aiStreamEvents.WithLabelValues(source).Add(float64(max(events, 0)))
		m.aiStreamBytes.WithLabelValues(source).Add(float64(max(bytes, 0)))
	}
}
func (m *Registry) AddAIStreamWindow(delta int) {
	if m != nil {
		m.aiStreamWindows.Add(float64(delta))
	}
}
