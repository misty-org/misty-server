package metrics

import "github.com/prometheus/client_golang/prometheus"

func (m *Registry) initWorkers() {
	m.workerWakes = prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "misty_worker_wakes_total", Help: "Worker wakeups by bounded queue and cause; idle empty queues have no timed wakeups.",
	}, []string{"queue", "reason"})
	m.registry.MustRegister(m.workerWakes)
}
func (m *Registry) RecordWorkerWake(queue, reason string) {
	if m == nil {
		return
	}
	switch queue {
	case "library-ai", "library-edit", "library-faces", "note-control", "drawing-control", "drawing-purge", "embedding", "social", "billing", "abuse-blocks", "abuse-retention", "agent-runtime", "agent-tasks", "scheduled", "ai-cleanup", "account-deletion", "rendition-reservations", "retention":
	default:
		return
	}
	switch reason {
	case "startup", "notification", "deadline", "error", "contention":
	default:
		return
	}
	m.workerWakes.WithLabelValues(queue, reason).Inc()
}
