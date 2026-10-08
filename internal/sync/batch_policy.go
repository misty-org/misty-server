package browsersync

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"

	db "github.com/kannachi323/misty/server/internal/platform/postgres"

	envconfig "github.com/kannachi323/misty/server/internal/platform/config"
)

// Batching: clients hold edits briefly and publish each burst as one op. The
// server owns the timings so load can be tuned without a client release; the
// account's sync frequency scales them. Only clients that connect with
// batching=1 receive the frame (older ones reject unknown frames).

// SyncFrequencySetting is the account preference that scales the policy.
const SyncFrequencySetting = "sync.frequency"

// BatchLane is one stream of outgoing changes, as the client sees it.
type BatchLane struct {
	QuietMs   int64 `json:"quiet_ms"`
	MaxWaitMs int64 `json:"max_wait_ms"`
	MaxEdits  int64 `json:"max_edits"`
}

// BatchPolicy is the base the presets scale. Revision lets operators push a
// changed base past a client's current one.
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

// presetScale multiplies quiet, max wait and max edits.
type presetScale struct{ quiet, wait, edits float64 }

var syncFrequencyPresets = map[string]presetScale{
	"live":     {quiet: 0.25, wait: 0.2, edits: 0.5},
	"balanced": {quiet: 1, wait: 1, edits: 1},
	"saver":    {quiet: 4, wait: 6, edits: 2},
}

// resolve scales the base by a preset; unknown presets are balanced.
func (p BatchPolicy) resolve(preset string, profileRevision int64) map[string]any {
	scale, ok := syncFrequencyPresets[preset]
	if !ok {
		preset, scale = "balanced", syncFrequencyPresets["balanced"]
	}
	lanes := make(map[string]BatchLane, len(p.Lanes))
	for name, lane := range p.Lanes {
		if name != "contended" {
			lane = clampLane(BatchLane{
				QuietMs:   int64(float64(lane.QuietMs) * scale.quiet),
				MaxWaitMs: int64(float64(lane.MaxWaitMs) * scale.wait),
				MaxEdits:  int64(float64(lane.MaxEdits) * scale.edits),
			})
		}
		lanes[name] = lane
	}
	// The base revision dominates, so an operator change outranks any
	// earlier account change; the account's own changes still increase it.
	return map[string]any{
		"revision": p.Revision<<32 | (profileRevision & 0xffffffff),
		"preset":   preset,
		"lanes":    lanes,
	}
}

// batchPolicyFrame is the frame for one account right now. When its
// preferences cannot be read it is the balanced base at revision 0, which a
// client already holding a newer policy ignores; err says so.
func (s *BrowserSyncService) batchPolicyFrame(ctx context.Context, userID string) (map[string]any, error) {
	preset, revision := "balanced", int64(0)
	prefs, err := s.database.AccountPreferences(ctx, userID)
	switch {
	case err == nil:
		if value, ok := prefs.Values[SyncFrequencySetting].(string); ok {
			preset = value
		}
		revision = prefs.Revision
	case errors.Is(err, db.ErrSettingsProfileNotFound):
		err = nil
	}
	return map[string]any{"type": "batch_policy", "policy": s.batchPolicy.resolve(preset, revision)}, err
}
