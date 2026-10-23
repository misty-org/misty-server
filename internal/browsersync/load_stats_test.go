package browsersync

import (
	"math"
	"os"
	"sort"
	"strconv"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/kannachi323/misty/server/internal/platform/db"
)

type loadStats struct {
	mu                          sync.Mutex
	acks, deltas                []time.Duration
	publishes, errors, discards atomic.Int64
}

func (s *loadStats) ack(d time.Duration) {
	s.mu.Lock()
	s.acks = append(s.acks, d)
	s.mu.Unlock()
}

func (s *loadStats) delta(d time.Duration) {
	s.mu.Lock()
	s.deltas = append(s.deltas, d)
	s.mu.Unlock()
}

type loadReport struct {
	Clients        int     `json:"clients"`
	Seconds        float64 `json:"seconds"`
	Publishes      int64   `json:"publishes"`
	PublishesPerS  float64 `json:"publishes_per_second"`
	Discards       int64   `json:"discards"`
	Errors         int64   `json:"errors"`
	AckP50Ms       float64 `json:"ack_p50_ms"`
	AckP99Ms       float64 `json:"ack_p99_ms"`
	Deltas         int     `json:"deltas"`
	DeltaP50Ms     float64 `json:"delta_p50_ms"`
	DeltaP95Ms     float64 `json:"delta_p95_ms"`
	DeltaP99Ms     float64 `json:"delta_p99_ms"`
	CommitsPerS    float64 `json:"db_commits_per_second"`
	WALSyncsPerS   float64 `json:"db_wal_syncs_per_second"`
	CommitsPerSync float64 `json:"db_commits_per_wal_sync"`
	HeapMB         float64 `json:"process_heap_mb"`
}

func (s *loadStats) report(duration time.Duration, clients int, dbDelta loadCounters, heap uint64) loadReport {
	s.mu.Lock()
	defer s.mu.Unlock()
	seconds := duration.Seconds()
	r := loadReport{
		Clients: clients, Seconds: seconds,
		Publishes: s.publishes.Load(), Discards: s.discards.Load(), Errors: s.errors.Load(),
		AckP50Ms: percentile(s.acks, 0.50), AckP99Ms: percentile(s.acks, 0.99),
		Deltas:     len(s.deltas),
		DeltaP50Ms: percentile(s.deltas, 0.50), DeltaP95Ms: percentile(s.deltas, 0.95), DeltaP99Ms: percentile(s.deltas, 0.99),
		CommitsPerS: float64(dbDelta.commits) / seconds, WALSyncsPerS: float64(dbDelta.walSyncs) / seconds,
		HeapMB: float64(heap) / (1 << 20),
	}
	r.PublishesPerS = float64(r.Publishes) / seconds
	if dbDelta.walSyncs > 0 {
		// Above 1: PostgreSQL already flushes several commits together.
		r.CommitsPerSync = float64(dbDelta.commits) / float64(dbDelta.walSyncs)
	}
	return r
}

func percentile(values []time.Duration, p float64) float64 {
	if len(values) == 0 {
		return 0
	}
	sorted := append([]time.Duration(nil), values...)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i] < sorted[j] })
	index := int(math.Ceil(p*float64(len(sorted)))) - 1
	return float64(sorted[max(index, 0)].Microseconds()) / 1000
}

type loadCounters struct{ commits, walSyncs int64 }

func (c loadCounters) minus(o loadCounters) loadCounters {
	return loadCounters{c.commits - o.commits, c.walSyncs - o.walSyncs}
}

// loadDatabaseCounters reads commit and WAL flush totals: their ratio shows
// how many commits PostgreSQL already groups into one disk flush.
func loadDatabaseCounters(t *testing.T, database *db.Database) loadCounters {
	t.Helper()
	var c loadCounters
	if err := database.Conn.QueryRow(`SELECT xact_commit FROM pg_stat_database WHERE datname=current_database()`).Scan(&c.commits); err != nil {
		t.Fatal(err)
	}
	// pg_stat_wal exists from PostgreSQL 14; without it the ratio is omitted.
	_ = database.Conn.QueryRow(`SELECT wal_sync FROM pg_stat_wal`).Scan(&c.walSyncs)
	return c
}

func loadParallel(t *testing.T, n, width int, work func(int) error) {
	t.Helper()
	var wg sync.WaitGroup
	var failed atomic.Pointer[error]
	next := atomic.Int64{}
	for range width {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				i := int(next.Add(1) - 1)
				if i >= n || failed.Load() != nil {
					return
				}
				if err := work(i); err != nil {
					failed.CompareAndSwap(nil, &err)
					return
				}
			}
		}()
	}
	wg.Wait()
	if err := failed.Load(); err != nil {
		t.Fatal(*err)
	}
}

func loadEnvInt(name string, fallback int) int {
	if v, err := strconv.Atoi(os.Getenv(name)); err == nil {
		return v
	}
	return fallback
}

func loadEnvFloat(name string, fallback float64) float64 {
	if v, err := strconv.ParseFloat(os.Getenv(name), 64); err == nil {
		return v
	}
	return fallback
}

func loadEnvDuration(name string, fallback time.Duration) time.Duration {
	if v, err := time.ParseDuration(os.Getenv(name)); err == nil {
		return v
	}
	return fallback
}
