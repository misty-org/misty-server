package main

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"strconv"
	"strings"
	"time"

	"golang.org/x/sys/unix"

	"github.com/misty-org/misty-server/internal/platform/config"
)

// watch is the alerts service of compose.yml. Every minute it checks that the
// API answers /health, how many of its requests failed with a server error
// (from /metrics), the free space on the database volume and, hourly, that a
// backup was made in the last 26 hours. A problem seen on two checks in a row
// becomes an open alert in the server_alerts table, which the console and the
// server owner's app show; it is removed when the problem clears. Alerts are
// never emailed. A write that fails (the database itself may be down) is
// tried again on the next check.

const (
	watchInterval   = time.Minute
	backupMaxAge    = 26 * time.Hour
	minFreeDisk     = 0.10
	errorShareAlarm = 0.05
	minRequests     = 20
)

// alertBook keeps the open alerts where the API and the console read them.
type alertBook interface {
	openAlerts(ctx context.Context) (map[string]bool, error)
	raise(ctx context.Context, probe, title, detail string) error
	clear(ctx context.Context, probe string) error
}

type probe struct {
	name, title string
	check       func(context.Context) string
}

type watcher struct {
	book     alertBook
	probes   []probe
	failures map[string]int
	open     map[string]bool
	loaded   bool
}

func runWatch(ctx context.Context) error {
	store, err := openStore(secretsDir())
	if err != nil {
		return err
	}
	book, err := openAlertBook(store)
	if err != nil {
		return err
	}
	defer book.close()
	api := strings.TrimRight(envOr("MISTY_WATCH_API_URL", "http://api:8080"), "/")
	client := &http.Client{Timeout: 10 * time.Second}
	errors := &errorShare{}
	started := time.Now()
	var lastBackupCheck time.Time
	lastBackupProblem := ""
	w := &watcher{
		book:     book,
		failures: map[string]int{}, open: map[string]bool{},
		probes: []probe{
			{"health", "The server is down", func(ctx context.Context) string { return checkHealth(ctx, client, api) }},
			{"errors", "The server is failing requests", func(ctx context.Context) string {
				return errors.check(ctx, client, api, store.Get("MISTY_METRICS_TOKEN"))
			}},
			{"disk", "The server is running out of disk", func(context.Context) string {
				return checkDisk(envOr("MISTY_DATA_DIR", "/var/lib/misty/pgdata"))
			}},
			{"backups", "The server has no recent backup", func(ctx context.Context) string {
				if time.Since(lastBackupCheck) >= time.Hour {
					lastBackupCheck = time.Now()
					lastBackupProblem = checkBackups(ctx, store, started)
				}
				return lastBackupProblem
			}},
		},
	}
	log.Printf("watching %s on %s; open alerts show in the console and the owner's app", api, config.Getenv("MISTY_HOSTNAME"))
	ticker := time.NewTicker(watchInterval)
	defer ticker.Stop()
	for {
		w.tick(ctx)
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
		}
	}
}

// tick runs every probe once. A problem seen on two checks in a row opens
// an alert, and its end closes it. The alerts open before a restart are read
// back first, so one that cleared meanwhile still closes.
func (w *watcher) tick(ctx context.Context) {
	if !w.loaded {
		if open, err := w.book.openAlerts(ctx); err == nil {
			w.open, w.loaded = open, true
		} else {
			log.Printf("read open alerts: %v", err)
		}
	}
	for _, p := range w.probes {
		problem := p.check(ctx)
		switch {
		case problem != "":
			w.failures[p.name]++
			if w.failures[p.name] < 2 || w.open[p.name] {
				continue
			}
			if err := w.book.raise(ctx, p.name, p.title, problem); err != nil {
				log.Printf("record alert %q: %v", p.title, err)
				continue
			}
			log.Printf("alert: %s. %s", p.title, problem)
			w.open[p.name] = true
		case w.open[p.name]:
			w.failures[p.name] = 0
			if err := w.book.clear(ctx, p.name); err != nil {
				log.Printf("clear alert %q: %v", p.title, err)
				continue
			}
			log.Printf("cleared: %s", p.title)
			w.open[p.name] = false
		default:
			w.failures[p.name] = 0
		}
	}
}

func checkHealth(ctx context.Context, client *http.Client, api string) string {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, api+"/health", nil)
	if err != nil {
		return err.Error()
	}
	resp, err := client.Do(req)
	if err != nil {
		return "The API did not answer its health check: " + err.Error()
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusOK {
		return ""
	}
	var body struct {
		Checks map[string]struct {
			Status   string `json:"status"`
			Critical bool   `json:"critical"`
		} `json:"checks"`
	}
	_ = json.NewDecoder(io.LimitReader(resp.Body, 1<<16)).Decode(&body)
	failing := []string{}
	for name, check := range body.Checks {
		if check.Critical && check.Status != "ok" {
			failing = append(failing, name+" ("+check.Status+")")
		}
	}
	return fmt.Sprintf("The API's health check answered %s. Failing: %s.", resp.Status, strings.Join(failing, ", "))
}

// errorShare compares the request counters between two checks.
type errorShare struct {
	total, failed float64
	known         bool
}

func (e *errorShare) check(ctx context.Context, client *http.Client, api, token string) string {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, api+"/metrics", nil)
	if err != nil || token == "" {
		return ""
	}
	req.Header.Set("Authorization", "Bearer "+token)
	resp, err := client.Do(req)
	if err != nil {
		return "" // The health probe reports an API that doesn't answer.
	}
	defer resp.Body.Close()
	total, failed := requestCounts(resp.Body)
	previousTotal, previousFailed, known := e.total, e.failed, e.known
	e.total, e.failed, e.known = total, failed, true
	requests, errors := total-previousTotal, failed-previousFailed
	if !known || requests < minRequests || requests < 0 || errors/requests < errorShareAlarm {
		return ""
	}
	return fmt.Sprintf("%.0f of the last %.0f requests failed with a server error.", errors, requests)
}

// requestCounts sums misty_http_requests_total, and its 5xx part, from
// Prometheus text.
func requestCounts(body io.Reader) (total, failed float64) {
	scanner := bufio.NewScanner(body)
	scanner.Buffer(make([]byte, 64<<10), 1<<20)
	for scanner.Scan() {
		line := scanner.Text()
		if !strings.HasPrefix(line, "misty_http_requests_total{") {
			continue
		}
		value, err := strconv.ParseFloat(line[strings.LastIndexByte(line, ' ')+1:], 64)
		if err != nil {
			continue
		}
		total += value
		if strings.Contains(line, `status="5xx"`) {
			failed += value
		}
	}
	return total, failed
}

func checkDisk(dir string) string {
	var stat unix.Statfs_t
	if err := unix.Statfs(dir, &stat); err != nil || stat.Blocks == 0 {
		return ""
	}
	free := float64(stat.Bavail) / float64(stat.Blocks)
	if free >= minFreeDisk {
		return ""
	}
	return fmt.Sprintf("The database volume has %.0f%% of its space free (%.1f GB).", free*100,
		float64(stat.Bavail)*float64(stat.Bsize)/1e9)
}

func checkBackups(ctx context.Context, store *Store, started time.Time) string {
	client, bucket, err := backupClient(store)
	if err != nil {
		return ""
	}
	keys, err := backupKeys(ctx, client, bucket)
	if err != nil {
		return "The backup bucket could not be listed: " + err.Error()
	}
	newest := time.Time{}
	for _, key := range keys {
		name := strings.TrimSuffix(strings.TrimPrefix(key, backupPrefix), ".dump.age")
		if at, err := time.Parse("2006-01-02T15-04-05Z", name); err == nil && at.After(newest) {
			newest = at
		}
	}
	if newest.IsZero() && time.Since(started) < backupMaxAge {
		return ""
	}
	if time.Since(newest) < backupMaxAge {
		return ""
	}
	if newest.IsZero() {
		return "No backup has been made. Check docker compose logs backups."
	}
	return "The newest backup is from " + newest.Format(time.RFC1123) + ". Check docker compose logs backups."
}
