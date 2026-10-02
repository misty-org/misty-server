package metrics

import (
	"context"

	"github.com/prometheus/client_golang/prometheus"
)

type registryContextKey struct{}

type syncTraffic struct {
	bytes       *prometheus.CounterVec
	messages    *prometheus.CounterVec
	connections prometheus.Gauge
}

func (m *Registry) initSyncTraffic() {
	labels := []string{"direction", "type"}
	m.syncTraffic = syncTraffic{
		bytes: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "misty_sync_payload_bytes_total",
			Help: "Browser sync application payload bytes; excludes WebSocket framing, TLS and transport overhead.",
		}, labels),
		messages: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "misty_sync_messages_total", Help: "Browser sync messages by bounded protocol type.",
		}, labels),
		connections: prometheus.NewGauge(prometheus.GaugeOpts{
			Name: "misty_sync_connections", Help: "Open browser sync WebSockets, including device-proof handshakes.",
		}),
	}
	m.registry.MustRegister(m.syncTraffic.bytes, m.syncTraffic.messages, m.syncTraffic.connections)
}

// TrackSyncConnection returns cleanup for one upgraded socket, even when proof fails.
func TrackSyncConnection(ctx context.Context) func() {
	m, _ := ctx.Value(registryContextKey{}).(*Registry)
	if m == nil {
		return func() {}
	}
	m.syncTraffic.connections.Inc()
	return func() { m.syncTraffic.connections.Dec() }
}

// RecordSyncMessage only accepts bounded labels; user-supplied types cannot grow cardinality.
func RecordSyncMessage(ctx context.Context, direction, kind string, size int) {
	m, _ := ctx.Value(registryContextKey{}).(*Registry)
	if m == nil || size < 0 || (direction != "in" && direction != "out") {
		return
	}
	switch kind {
	case "challenge", "authenticate", "welcome", "publish", "heartbeat", "resume", "ack", "error",
		"checkpoint_required", "devices", "events", "presence", "workspaces", "account_event",
		"watch_workspace", "unwatch_workspace", "claim", "publish_workspace", "workspace_ack",
		"workspace_current", "workspace_delta", "workspace_error", "workspace_snapshot", "slot",
		"slot_get", "blob_put", "blob_get", "records_pull", "records_push",
		"blob_ack", "blobs", "records", "records_ack", "ping", "pong":
	default:
		kind = "other"
	}
	m.syncTraffic.bytes.WithLabelValues(direction, kind).Add(float64(size))
	m.syncTraffic.messages.WithLabelValues(direction, kind).Inc()
}
