package console

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"

	dto "github.com/prometheus/client_model/go"
	"github.com/prometheus/common/expfmt"
	"github.com/prometheus/common/model"
)

var errMetricsNotConfigured = errors.New("set MISTY_METRICS_TOKEN to show traffic metrics")

// MetricsSummary is the handful of numbers the Overview shows. Rates are
// computed between consecutive scrapes, so the first view has no rate yet.
type MetricsSummary struct {
	HasRate        bool
	RequestsPerMin float64
	ErrorRate      float64
	InFlight       float64
	AIInvocations  float64
}

type metricsSample struct {
	at        time.Time
	requests  float64
	serverErr float64
}

// metricsSampler scrapes misty-server's bearer-protected /metrics endpoint
// and remembers the previous sample to derive per-minute rates.
type metricsSampler struct {
	url    string
	token  string
	client *http.Client
	mu     sync.Mutex
	prev   *metricsSample
}

func newMetricsSampler(apiURL, token string, client *http.Client) *metricsSampler {
	return &metricsSampler{url: strings.TrimRight(apiURL, "/") + "/metrics", token: token, client: client}
}

func (m *metricsSampler) Sample(ctx context.Context) (MetricsSummary, error) {
	var summary MetricsSummary
	if m.token == "" {
		return summary, errMetricsNotConfigured
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, m.url, nil)
	if err != nil {
		return summary, err
	}
	req.Header.Set("Authorization", "Bearer "+m.token)
	resp, err := m.client.Do(req)
	if err != nil {
		return summary, fmt.Errorf("metrics unreachable at %s", m.url)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return summary, fmt.Errorf("metrics returned HTTP %d; check MISTY_METRICS_TOKEN", resp.StatusCode)
	}
	parser := expfmt.NewTextParser(model.UTF8Validation)
	families, err := parser.TextToMetricFamilies(resp.Body)
	if err != nil {
		return summary, fmt.Errorf("could not parse metrics: %w", err)
	}

	current := metricsSample{at: time.Now()}
	current.requests, current.serverErr = requestTotals(families["misty_http_requests_total"])
	summary.InFlight = gaugeValue(families["misty_http_requests_in_flight"])
	summary.AIInvocations = counterSum(families["misty_ai_invocations_total"])

	m.mu.Lock()
	prev := m.prev
	m.prev = &current
	m.mu.Unlock()

	if prev != nil && current.requests >= prev.requests {
		elapsed := current.at.Sub(prev.at).Minutes()
		delta := current.requests - prev.requests
		if elapsed > 0 {
			summary.HasRate = true
			summary.RequestsPerMin = delta / elapsed
			if delta > 0 {
				summary.ErrorRate = (current.serverErr - prev.serverErr) / delta
			}
		}
	} else if current.requests > 0 {
		summary.ErrorRate = current.serverErr / current.requests
	}
	return summary, nil
}

// requestTotals sums all requests and those whose status label is a 5xx.
// The server labels status by class ("5xx") but exact codes are accepted too.
func requestTotals(family *dto.MetricFamily) (total, serverErr float64) {
	if family == nil {
		return 0, 0
	}
	for _, metric := range family.GetMetric() {
		value := metric.GetCounter().GetValue()
		total += value
		for _, label := range metric.GetLabel() {
			if label.GetName() == "status" && strings.HasPrefix(label.GetValue(), "5") {
				serverErr += value
			}
		}
	}
	return total, serverErr
}

func counterSum(family *dto.MetricFamily) float64 {
	var sum float64
	if family != nil {
		for _, metric := range family.GetMetric() {
			sum += metric.GetCounter().GetValue()
		}
	}
	return sum
}

func gaugeValue(family *dto.MetricFamily) float64 {
	if family == nil || len(family.GetMetric()) == 0 {
		return 0
	}
	return family.GetMetric()[0].GetGauge().GetValue()
}
