package main

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
)

// memoryBook is an alert table in memory; failing makes every write fail.
type memoryBook struct {
	rows    map[string]string
	events  []string
	failing bool
}

func (b *memoryBook) openAlerts(context.Context) (map[string]bool, error) {
	open := map[string]bool{}
	for probe := range b.rows {
		open[probe] = true
	}
	return open, nil
}

func (b *memoryBook) raise(_ context.Context, probe, title, _ string) error {
	if b.failing {
		return errors.New("database unavailable")
	}
	b.rows[probe] = title
	b.events = append(b.events, "open "+title)
	return nil
}

func (b *memoryBook) clear(_ context.Context, probe string) error {
	if b.failing {
		return errors.New("database unavailable")
	}
	b.events = append(b.events, "clear "+b.rows[probe])
	delete(b.rows, probe)
	return nil
}

func TestWatchOpensAnAlertOnTheSecondFailureAndClearsIt(t *testing.T) {
	var healthy atomic.Bool
	healthy.Store(true)
	api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if healthy.Load() {
			w.WriteHeader(http.StatusOK)
			return
		}
		w.WriteHeader(http.StatusServiceUnavailable)
		_, _ = w.Write([]byte(`{"checks":{"database":{"status":"unavailable","critical":true},"public_api":{"status":"unconfigured","critical":false}}}`))
	}))
	defer api.Close()
	book := &memoryBook{rows: map[string]string{}}
	w := &watcher{book: book, failures: map[string]int{}, open: map[string]bool{},
		probes: []probe{{"health", "The server is down", func(ctx context.Context) string { return checkHealth(ctx, api.Client(), api.URL) }}}}
	ctx := context.Background()
	w.tick(ctx)
	healthy.Store(false)
	w.tick(ctx)
	if len(book.events) != 0 {
		t.Fatalf("alerted after one failure: %v", book.events)
	}
	// A write that fails is tried again on the next check.
	book.failing = true
	w.tick(ctx)
	book.failing = false
	w.tick(ctx)
	w.tick(ctx)
	if len(book.events) != 1 || book.events[0] != "open The server is down" {
		t.Fatalf("events = %v", book.events)
	}
	healthy.Store(true)
	w.tick(ctx)
	w.tick(ctx)
	if len(book.events) != 2 || book.events[1] != "clear The server is down" || len(book.rows) != 0 {
		t.Fatalf("events = %v, open = %v", book.events, book.rows)
	}
	if problem := checkHealth(ctx, api.Client(), api.URL); problem != "" {
		t.Fatalf("healthy = %q", problem)
	}
	healthy.Store(false)
	if problem := checkHealth(ctx, api.Client(), api.URL); !strings.Contains(problem, "database (unavailable)") || strings.Contains(problem, "public_api") {
		t.Fatalf("problem = %q", problem)
	}
}

// An alert open before a restart still clears when its problem has gone.
func TestWatchClearsAnAlertOpenBeforeARestart(t *testing.T) {
	book := &memoryBook{rows: map[string]string{"disk": "The server is running out of disk"}}
	w := &watcher{book: book, failures: map[string]int{}, open: map[string]bool{},
		probes: []probe{{"disk", "The server is running out of disk", func(context.Context) string { return "" }}}}
	w.tick(context.Background())
	if len(book.rows) != 0 {
		t.Fatalf("still open: %v", book.rows)
	}
}

func TestErrorShareComparesCountersBetweenChecks(t *testing.T) {
	var body atomic.Value
	body.Store("")
	api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer token" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		_, _ = w.Write([]byte(body.Load().(string)))
	}))
	defer api.Close()
	metrics := func(ok, failed int) string {
		return "# TYPE misty_http_requests_total counter\n" +
			`misty_http_requests_total{method="GET",route="/v1/me",status="2xx"} ` + itoa(ok) + "\n" +
			`misty_http_requests_total{method="POST",route="/v1/sync",status="5xx"} ` + itoa(failed) + "\n"
	}
	share := &errorShare{}
	ctx := context.Background()
	body.Store(metrics(100, 1))
	if problem := share.check(ctx, api.Client(), api.URL, "token"); problem != "" {
		t.Fatalf("first read = %q", problem)
	}
	body.Store(metrics(190, 2))
	if problem := share.check(ctx, api.Client(), api.URL, "token"); problem != "" {
		t.Fatalf("1 of 91 failed = %q", problem)
	}
	body.Store(metrics(200, 22))
	if problem := share.check(ctx, api.Client(), api.URL, "token"); problem != "20 of the last 30 requests failed with a server error." {
		t.Fatalf("20 of 30 failed = %q", problem)
	}
}

func itoa(n int) string { return strconv.Itoa(n) }
