package browsersync

import (
	"encoding/json"
	"fmt"
	"strconv"

	envconfig "github.com/kannachi323/misty/server/internal/platform/config"
)

// Batching: clients hold edits briefly and publish each burst as one op. The
// server owns the base timings so load can be tuned without a client release; each
// device scales them by its own sync frequency, which never reaches the server. Only
// clients that connect with batching=1 receive the frame (older ones reject unknown
// frames).

// BatchLane is one stream of outgoing changes, as the client sees it.
type BatchLane struct {
	QuietMs   int64 `json:"quiet_ms"`
	MaxWaitMs int64 `json:"max_wait_ms"`
	MaxEdits  int64 `json:"max_edits"`
}

// BatchPolicy is the base each device scales. Revision lets operators push a changed
// base past a client's current one.
type BatchPolicy struct {
	Revision int64
	Lanes    map[string]BatchLane
}

func defaultBatchPolicy() BatchPolicy {
	return BatchPolicy{Revision: 1, Lanes: map[string]BatchLane{
		"workspace":   {QuietMs: 1_000, MaxWaitMs: 5_000, MaxEdits: 50},
		"slots":       {QuietMs: 3_000, MaxWaitMs: 15_000, MaxEdits: 100},
		"collections": {QuietMs: 1_000, MaxWaitMs: 5_000, MaxEdits: 50},
		"history":     {QuietMs: 10_000, MaxWaitMs: 60_000, MaxEdits: 200},
		// Another machine is editing the same workspace: near-live, never scaled.
		"contended": {QuietMs: 250, MaxWaitMs: 1_000, MaxEdits: 50},
	}}
}

// batchPolicyFromEnv overlays MISTY_SYNC_BATCH_LANES (a JSON object of lanes)
// on the defaults. MISTY_SYNC_BATCH_REVISION must grow whenever it changes.
func batchPolicyFromEnv() (BatchPolicy, error) {
	policy := defaultBatchPolicy()
	if raw := envconfig.Getenv("MISTY_SYNC_BATCH_REVISION"); raw != "" {
		revision, err := strconv.ParseInt(raw, 10, 64)
		if err != nil || revision < 1 || revision > 1<<30 {
			return defaultBatchPolicy(), fmt.Errorf("MISTY_SYNC_BATCH_REVISION must be a positive integer")
		}
		policy.Revision = revision
	}
	if raw := envconfig.Getenv("MISTY_SYNC_BATCH_LANES"); raw != "" {
		var lanes map[string]BatchLane
		if err := json.Unmarshal([]byte(raw), &lanes); err != nil {
			return defaultBatchPolicy(), fmt.Errorf("MISTY_SYNC_BATCH_LANES is not valid JSON: %w", err)
		}
		for name, lane := range lanes {
			if _, known := policy.Lanes[name]; !known {
				return defaultBatchPolicy(), fmt.Errorf("MISTY_SYNC_BATCH_LANES names unknown lane %q", name)
			}
			policy.Lanes[name] = clampLane(lane)
		}
	}
	return policy, nil
}

// clampLane keeps values inside what clients accept (they clamp the same way).
func clampLane(l BatchLane) BatchLane {
	l.QuietMs = min(max(l.QuietMs, 100), 60_000)
	l.MaxWaitMs = max(min(max(l.MaxWaitMs, 250), 300_000), l.QuietMs)
	l.MaxEdits = min(max(l.MaxEdits, 1), 1_000)
	return l
}

// frame is the batch_policy frame: the base lanes, as they are.
func (p BatchPolicy) frame() map[string]any {
	lanes := make(map[string]BatchLane, len(p.Lanes))
	for name, lane := range p.Lanes {
		lanes[name] = clampLane(lane)
	}
	return map[string]any{"type": "batch_policy", "policy": map[string]any{
		"revision": p.Revision,
		"lanes":    lanes,
	}}
}

// batchPolicyFrame is the frame sent to every client right now.
func (s *BrowserSyncService) batchPolicyFrame() map[string]any {
	return s.batchPolicy.frame()
}
