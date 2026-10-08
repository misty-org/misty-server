package browsersync

import (
	"sync"
	"time"
)

// Per-device budget for publishes (workspace ops and collection pushes) from
// batching clients. Balanced batching sends about one op every few seconds
// under continuous use; the budget allows bursts of contended, near-live
// editing and retries, and turns away a client that ignores its policy.
// Older clients are not limited here: they would treat the rejection as a
// bad edit and drop it. The per-socket frame cap still bounds them.
const (
	syncPublishBurst  = 60
	syncPublishRefill = time.Second
	// Buckets idle this long are full again and can be forgotten.
	syncBucketIdle = 2 * syncPublishBurst * syncPublishRefill
)

type publishBucket struct {
	tokens float64
	at     time.Time
}

type publishLimiter struct {
	mu      sync.Mutex
	devices map[string]*publishBucket
	swept   time.Time
}

// take spends one publish for device, or reports how long until one is
// available.
func (l *publishLimiter) take(device string, now time.Time) (bool, time.Duration) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.devices == nil {
		l.devices = map[string]*publishBucket{}
	}
	if now.Sub(l.swept) >= syncBucketIdle {
		for id, bucket := range l.devices {
			if now.Sub(bucket.at) >= syncBucketIdle {
				delete(l.devices, id)
			}
		}
		l.swept = now
	}
	bucket, ok := l.devices[device]
	if !ok {
		bucket = &publishBucket{tokens: syncPublishBurst, at: now}
		l.devices[device] = bucket
	}
	bucket.tokens = min(syncPublishBurst, bucket.tokens+now.Sub(bucket.at).Seconds()/syncPublishRefill.Seconds())
	bucket.at = now
	if bucket.tokens >= 1 {
		bucket.tokens--
		return true, 0
	}
	wait := time.Duration((1 - bucket.tokens) * float64(syncPublishRefill))
	return false, max(wait, time.Second)
}
