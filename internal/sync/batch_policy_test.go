package browsersync

import (
	"testing"
	"time"
)

func TestBatchPolicyPresetsScaleEveryLaneButContended(t *testing.T) {
	base := defaultBatchPolicy()
	saver := base.resolve("saver", 7)
	lanes := saver["lanes"].(map[string]BatchLane)
	if got := lanes["workspace"]; got != (BatchLane{QuietMs: 4_000, MaxWaitMs: 30_000, MaxEdits: 100}) {
		t.Fatalf("saver workspace lane = %+v", got)
	}
	if got := lanes["contended"]; got != base.Lanes["contended"] {
		t.Fatalf("contended lane must not scale, got %+v", got)
	}
	// History under saver: 10 s × 4 and 60 s × 6, clamped to the client's bounds.
	if got := lanes["history"]; got != (BatchLane{QuietMs: 40_000, MaxWaitMs: 300_000, MaxEdits: 400}) {
		t.Fatalf("saver history lane = %+v", got)
	}
	live := base.resolve("live", 7)["lanes"].(map[string]BatchLane)
	if got := live["workspace"]; got != (BatchLane{QuietMs: 250, MaxWaitMs: 1_000, MaxEdits: 25}) {
		t.Fatalf("live workspace lane = %+v", got)
	}
	unknown := base.resolve("turbo", 7)
	if unknown["preset"] != "balanced" {
		t.Fatalf("unknown preset resolved to %v", unknown["preset"])
	}
	if saver["revision"].(int64) != 1<<32|7 {
		t.Fatalf("revision = %v", saver["revision"])
	}
}

func TestBatchPolicyFromEnvClampsAndRejectsUnknownLanes(t *testing.T) {
	t.Setenv("MISTY_SYNC_BATCH_REVISION", "3")
	t.Setenv("MISTY_SYNC_BATCH_LANES", `{"workspace":{"quiet_ms":10,"max_wait_ms":5,"max_edits":0}}`)
	policy, err := batchPolicyFromEnv()
	if err != nil {
		t.Fatal(err)
	}
	if policy.Revision != 3 || policy.Lanes["workspace"] != (BatchLane{QuietMs: 100, MaxWaitMs: 250, MaxEdits: 1}) {
		t.Fatalf("policy = %+v", policy)
	}
	t.Setenv("MISTY_SYNC_BATCH_LANES", `{"bogus":{"quiet_ms":1,"max_wait_ms":1,"max_edits":1}}`)
	if _, err := batchPolicyFromEnv(); err == nil {
		t.Fatal("an unknown lane must be rejected")
	}
}

func TestPublishLimiterAllowsABurstThenAsksForAWait(t *testing.T) {
	var limiter publishLimiter
	now := time.Unix(1_700_000_000, 0)
	for i := 0; i < syncPublishBurst; i++ {
		if ok, _ := limiter.take("device", now); !ok {
			t.Fatalf("publish %d of the burst was refused", i)
		}
	}
	ok, wait := limiter.take("device", now)
	if ok || wait < time.Second {
		t.Fatalf("over budget: ok=%v wait=%v", ok, wait)
	}
	if ok, _ := limiter.take("other", now); !ok {
		t.Fatal("devices have separate budgets")
	}
	if ok, _ := limiter.take("device", now.Add(syncPublishRefill)); !ok {
		t.Fatal("the budget refills over time")
	}
	// Idle buckets are forgotten.
	limiter.take("third", now.Add(syncBucketIdle*2))
	if _, kept := limiter.devices["device"]; kept {
		t.Fatal("an idle bucket was kept")
	}
}
