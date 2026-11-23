package console

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"sort"
	"strings"
	"time"
)

type HealthCheck struct {
	Name       string
	Status     string `json:"status"`
	Critical   bool   `json:"critical"`
	Message    string `json:"message,omitempty"`
	DurationMS int64  `json:"duration_ms,omitempty"`
}

type HealthSnapshot struct {
	Status         string                 `json:"status"`
	Version        string                 `json:"version"`
	Environment    string                 `json:"environment"`
	ReleaseChannel string                 `json:"release_channel"`
	UptimeSeconds  int64                  `json:"uptime_seconds"`
	Checks         map[string]HealthCheck `json:"checks"`
}

func (s HealthSnapshot) Healthy() bool {
	return s.Status == "ok"
}

func (s HealthSnapshot) SortedChecks() []HealthCheck {
	checks := make([]HealthCheck, 0, len(s.Checks))
	for name, check := range s.Checks {
		check.Name = name
		checks = append(checks, check)
	}
	sort.Slice(checks, func(i, j int) bool {
		iOK, jOK := checks[i].OK(), checks[j].OK()
		if iOK != jOK {
			return !iOK
		}
		return checks[i].Name < checks[j].Name
	})
	return checks
}

func (c HealthCheck) OK() bool {
	switch c.Status {
	case "ok", "ready", "disabled":
		return true
	}
	return false
}

func (c HealthCheck) Label() string {
	words := strings.Split(c.Name, "_")
	for i, word := range words {
		switch word {
		case "api", "ai", "mcp", "url":
			words[i] = strings.ToUpper(word)
		}
	}
	label := strings.Join(words, " ")
	if label == "" {
		return label
	}
	return strings.ToUpper(label[:1]) + label[1:]
}

func (s HealthSnapshot) Uptime() string {
	d := time.Duration(s.UptimeSeconds) * time.Second
	switch {
	case d >= 24*time.Hour:
		return fmt.Sprintf("%dd %dh", int(d.Hours())/24, int(d.Hours())%24)
	case d >= time.Hour:
		return fmt.Sprintf("%dh %dm", int(d.Hours()), int(d.Minutes())%60)
	default:
		return fmt.Sprintf("%dm", int(d.Minutes()))
	}
}

type healthClient struct {
	url    string
	client *http.Client
}

func newHealthClient(apiURL string, client *http.Client) *healthClient {
	return &healthClient{url: strings.TrimRight(apiURL, "/") + "/health", client: client}
}

func (h *healthClient) Fetch(ctx context.Context) (HealthSnapshot, error) {
	var snapshot HealthSnapshot
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, h.url, nil)
	if err != nil {
		return snapshot, err
	}
	resp, err := h.client.Do(req)
	if err != nil {
		return snapshot, fmt.Errorf("server unreachable at %s", h.url)
	}
	defer resp.Body.Close()
	if err := json.NewDecoder(resp.Body).Decode(&snapshot); err != nil {
		return snapshot, fmt.Errorf("unexpected health response (HTTP %d)", resp.StatusCode)
	}
	return snapshot, nil
}
