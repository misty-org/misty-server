package app

import (
	"context"
	"strings"

	envconfig "github.com/kannachi323/misty/server/internal/platform/config"

	"github.com/kannachi323/misty/server/internal/platform/metrics"
)

// registerDomainGauges wires the counters that only this application knows.
//
// Everything here is a leading indicator. System metrics tell you the box is
// already struggling; a queue depth that has been climbing for ten minutes
// tells you why, and tells you before the requests start failing.
func (s *Server) registerDomainGauges(registry *metrics.Registry) {
	registry.WatchGauge(
		"misty_db_connections_open",
		"PostgreSQL connections currently open from this API process.",
		func(context.Context) (float64, error) {
			return float64(s.Database.Conn.Stats().OpenConnections), nil
		},
	)
	registry.WatchGauge(
		"misty_db_connections_in_use",
		"PostgreSQL connections currently checked out by API work.",
		func(context.Context) (float64, error) {
			return float64(s.Database.Conn.Stats().InUse), nil
		},
	)
	registry.WatchGauge(
		"misty_db_connections_max",
		"Configured PostgreSQL connection ceiling for this API process.",
		func(context.Context) (float64, error) {
			return float64(s.Database.Conn.Stats().MaxOpenConnections), nil
		},
	)
	registry.WatchGauge(
		"misty_realtime_connections",
		"Realtime WebSockets currently held open. An edge proxy sees only the upgrade, never the live count.",
		func(context.Context) (float64, error) {
			return float64(s.Realtime.ConnectionCount()), nil
		},
	)
	registry.WatchGauge(
		"misty_realtime_viewed_spaces",
		"Spaces with at least one connected viewer.",
		func(context.Context) (float64, error) {
			return float64(s.Realtime.ViewerSpaceCount()), nil
		},
	)
	registry.WatchGauge(
		"misty_note_control_backlog",
		"Collaboration control commands that are due and still undelivered. A sustained non-zero value means note revocations are not reaching the collaboration service.",
		func(ctx context.Context) (float64, error) {
			backlog, err := s.Database.NoteControlBacklog(ctx)
			return float64(backlog), err
		},
	)
	registry.WatchGauge(
		"misty_upload_reservations_active",
		"Uploads holding reserved quota without having finalized. A number that climbs and never falls means abandoned uploads are outpacing cleanup.",
		func(ctx context.Context) (float64, error) {
			count, err := s.Database.ActiveUploadReservations(ctx)
			return float64(count), err
		},
	)
	registry.WatchGauge(
		"misty_active_users",
		"Users holding an unexpired session. This counts real accounts, unlike an edge proxy's IP and user-agent heuristic.",
		func(ctx context.Context) (float64, error) {
			count, err := s.Database.ActiveUserCount(ctx)
			return float64(count), err
		},
	)

	// The background workers each drain one job kind. Reporting them separately
	// is what distinguishes "ffmpeg is wedged" from "the AI provider is slow".
	for _, kind := range []string{"ai", "rendition", "people"} {
		jobKind := kind
		registry.WatchGauge(
			"misty_library_jobs_pending_"+jobKind,
			"Library "+jobKind+" jobs queued, leased, or running.",
			func(ctx context.Context) (float64, error) {
				counts, err := s.Database.PendingLibraryJobs(ctx)
				if err != nil {
					return 0, err
				}
				return float64(counts[jobKind]), nil
			},
		)
	}
}

// metricsToken reads the bearer token that gates the metrics endpoint.
//
// When it is unset the endpoint is not mounted at all. The output names every
// route, its traffic volume, and its error rate, so exposing it by default
// would be a real disclosure.
func metricsToken() string {
	return strings.TrimSpace(envconfig.Getenv("MISTY_METRICS_TOKEN"))
}
