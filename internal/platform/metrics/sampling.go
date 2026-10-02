package metrics

import (
	"context"
	"github.com/prometheus/client_golang/prometheus"
)

// WatchGauges shares one database sample across several fixed domain gauges.
// All values retain their previous sample if the query fails.
func (m *Registry) WatchGauges(definitions map[string]string, read func(context.Context) (map[string]float64, error)) {
	gauges := make(map[string]prometheus.Gauge, len(definitions))
	for name, help := range definitions {
		gauge := prometheus.NewGauge(prometheus.GaugeOpts{Name: name, Help: help})
		m.registry.MustRegister(gauge)
		gauges[name] = gauge
	}
	m.mu.Lock()
	m.samplers = append(m.samplers, sampler{read: func(ctx context.Context) error {
		values, err := read(ctx)
		if err != nil {
			return err
		}
		for name, gauge := range gauges {
			gauge.Set(values[name])
		}
		return nil
	}})
	m.mu.Unlock()
}
