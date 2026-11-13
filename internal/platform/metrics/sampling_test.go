package metrics

import (
	"context"
	"errors"
	"testing"
	"time"
)

func sampleValue(t *testing.T, m *Registry, name string) float64 {
	t.Helper()
	families, err := m.registry.Gather()
	if err != nil {
		t.Fatal(err)
	}
	for _, family := range families {
		if family.GetName() == name {
			return family.Metric[0].GetGauge().GetValue()
		}
	}
	t.Fatalf("missing %s", name)
	return 0
}

func TestSamplingSharesQueriesAndDoesNotRefreshFailedAge(t *testing.T) {
	m := New()
	reads, failing := 0, false
	m.WatchGauges(map[string]string{"test_a": "a", "test_b": "b"}, func(context.Context) (map[string]float64, error) {
		reads++
		if failing {
			return nil, errors.New("database unavailable")
		}
		return map[string]float64{"test_a": 3, "test_b": 5}, nil
	})
	m.sampleOnce(context.Background())
	if reads != 1 || sampleValue(t, m, "test_a") != 3 || sampleValue(t, m, "test_b") != 5 {
		t.Fatal("group not sampled once")
	}
	m.lastSample = time.Now().Add(-time.Hour)
	failing = true
	m.sampleOnce(context.Background())
	if sampleValue(t, m, "misty_metrics_sample_age_seconds") < 3600 {
		t.Fatal("failed sample marked fresh")
	}
	if sampleValue(t, m, "test_b") != 5 {
		t.Fatal("failure erased last successful sample")
	}
	failing = false
	m.sampleOnce(context.Background())
	if sampleValue(t, m, "misty_metrics_sample_age_seconds") > 1 {
		t.Fatal("successful sample did not refresh age")
	}
}
