package metrics

import (
	"bufio"
	"context"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	. "github.com/kannachi323/misty/server/internal/platform/metrics"

	"github.com/go-chi/chi/v5"
)

func scrape(t *testing.T, registry *Registry, token string) string {
	t.Helper()
	request := httptest.NewRequest(http.MethodGet, "/metrics", nil)
	if token != "" {
		request.Header.Set("Authorization", "Bearer "+token)
	}
	recorder := httptest.NewRecorder()
	registry.Handler(token).ServeHTTP(recorder, request)
	if recorder.Code != http.StatusOK {
		t.Fatalf("scrape status = %d, want 200", recorder.Code)
	}
	return recorder.Body.String()
}

// Tier 1: the Go runtime collectors are what reveal a goroutine leak, which is
// invisible from outside the process.
func TestRuntimeMetricsArePresent(t *testing.T) {
	body := scrape(t, New(), "secret-token")

	for _, name := range []string{"go_goroutines", "go_memstats_heap_inuse_bytes", "process_open_fds"} {
		if !strings.Contains(body, name) {
			t.Fatalf("scrape is missing %s", name)
		}
	}
}

// Tier 2: labels must be the route template, never the concrete URL. Labelling
// by raw path would mint a new time series per Space id and blow up the metrics
// store within a day.
func TestRequestsAreLabelledByRoutePatternNotRawPath(t *testing.T) {
	registry := New()
	router := chi.NewRouter()
	router.Use(registry.Middleware)
	router.Get("/spaces/{spaceID}/tasks", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	for _, spaceID := range []string{"space_aaa", "space_bbb", "space_ccc"} {
		recorder := httptest.NewRecorder()
		router.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/spaces/"+spaceID+"/tasks", nil))
	}

	body := scrape(t, registry, "secret-token")
	if !strings.Contains(body, `route="/spaces/{spaceID}/tasks"`) {
		t.Fatalf("scrape is missing the route pattern label:\n%s", body)
	}
	for _, spaceID := range []string{"space_aaa", "space_bbb", "space_ccc"} {
		if strings.Contains(body, spaceID) {
			t.Fatalf("a concrete space id leaked into a label: %s", spaceID)
		}
	}
}

// Unmatched paths share one bucket, so a scanner probing random URLs cannot
// inflate cardinality at will.
func TestUnmatchedRequestsShareOneLabel(t *testing.T) {
	registry := New()
	router := chi.NewRouter()
	router.Use(registry.Middleware)
	router.Get("/known", func(w http.ResponseWriter, _ *http.Request) {})

	for _, path := range []string{"/wp-admin", "/.env", "/random-probe-12345"} {
		router.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, path, nil))
	}

	body := scrape(t, registry, "secret-token")
	if !strings.Contains(body, `route="unmatched"`) {
		t.Fatalf("unmatched requests were not bucketed:\n%s", body)
	}
	for _, path := range []string{"wp-admin", "random-probe-12345"} {
		if strings.Contains(body, path) {
			t.Fatalf("probe path %q became its own label", path)
		}
	}
}

func TestStatusIsRecordedAsAClass(t *testing.T) {
	registry := New()
	router := chi.NewRouter()
	router.Use(registry.Middleware)
	router.Get("/boom", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	})
	router.Get("/fine", func(w http.ResponseWriter, _ *http.Request) {})

	router.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/boom", nil))
	router.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/fine", nil))

	body := scrape(t, registry, "secret-token")
	if !strings.Contains(body, `status="5xx"`) || !strings.Contains(body, `status="2xx"`) {
		t.Fatalf("status classes missing:\n%s", body)
	}
	// A handler that writes no header still counts as 200, matching what
	// net/http actually sends.
	if strings.Contains(body, `status="0xx"`) {
		t.Fatal("a handler that never wrote a header was recorded as 0xx")
	}
}

// hijackableRecorder reports whether the wrapped writer preserved Hijacker.
type hijackableRecorder struct {
	*httptest.ResponseRecorder
	hijacked bool
}

func (r *hijackableRecorder) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	r.hijacked = true
	return nil, nil, errors.New("test hijack")
}

// The realtime endpoint upgrades to a WebSocket, which needs to take over the
// connection. A ResponseWriter wrapper that drops http.Hijacker would break
// every realtime connection while leaving ordinary requests working, which is
// exactly the kind of bug that reaches production.
func TestMiddlewarePreservesHijackerForWebSocketUpgrades(t *testing.T) {
	registry := New()
	var sawHijacker bool
	handler := registry.Middleware(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, sawHijacker = w.(http.Hijacker)
	}))

	recorder := &hijackableRecorder{ResponseRecorder: httptest.NewRecorder()}
	handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/api/realtime", nil))

	if !sawHijacker {
		t.Fatal("the wrapped ResponseWriter is not an http.Hijacker; WebSocket upgrades would fail")
	}
}

// The endpoint names every route and its traffic volume, so it must not be
// reachable without the token.
func TestMetricsRequireTheBearerToken(t *testing.T) {
	registry := New()
	handler := registry.Handler("secret-token")

	cases := map[string]string{
		"no header":     "",
		"wrong token":   "Bearer wrong-token",
		"bare token":    "secret-token",
		"wrong scheme":  "Basic secret-token",
		"short prefix":  "Bearer secret",
		"longer suffix": "Bearer secret-token-extra",
	}
	for name, header := range cases {
		request := httptest.NewRequest(http.MethodGet, "/metrics", nil)
		if header != "" {
			request.Header.Set("Authorization", header)
		}
		recorder := httptest.NewRecorder()
		handler.ServeHTTP(recorder, request)
		// 404 rather than 401: an unauthenticated caller should not learn that
		// a metrics endpoint exists here at all.
		if recorder.Code != http.StatusNotFound {
			t.Fatalf("%s: status = %d, want 404", name, recorder.Code)
		}
		if strings.Contains(recorder.Body.String(), "go_goroutines") {
			t.Fatalf("%s: metrics leaked to an unauthorized caller", name)
		}
	}
}

func TestUnconfiguredTokenRefusesEveryRequest(t *testing.T) {
	handler := New().Handler("")

	request := httptest.NewRequest(http.MethodGet, "/metrics", nil)
	request.Header.Set("Authorization", "Bearer anything")
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, request)

	if recorder.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404 when no token is configured", recorder.Code)
	}
}

// Tier 3: domain gauges are refreshed by the sampler, never during a scrape, so
// a slow query cannot turn monitoring into load on the system being monitored.
func TestDomainGaugesAreSampledOutsideTheScrape(t *testing.T) {
	registry := New()
	reads := make(chan struct{}, 8)

	registry.WatchGauge("misty_test_depth", "Test queue depth.", func(context.Context) (float64, error) {
		select {
		case reads <- struct{}{}:
		default:
		}
		return 42, nil
	})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	registry.StartSampling(ctx, 50*time.Millisecond)

	select {
	case <-reads:
	case <-time.After(2 * time.Second):
		t.Fatal("the sampler never called the gauge function")
	}

	body := scrape(t, registry, "secret-token")
	if !strings.Contains(body, "misty_test_depth 42") {
		t.Fatalf("sampled gauge missing from scrape:\n%s", body)
	}
}

// A failing sample must leave the previous value in place and surface as a
// failure counter, rather than silently reporting zero.
func TestFailedSampleDoesNotReportZero(t *testing.T) {
	registry := New()
	fail := make(chan bool, 1)
	fail <- false

	registry.WatchGauge("misty_test_flaky", "Flaky gauge.", func(context.Context) (float64, error) {
		shouldFail := <-fail
		fail <- true
		if shouldFail {
			return 0, errors.New("database unavailable")
		}
		return 7, nil
	})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	registry.StartSampling(ctx, 30*time.Millisecond)
	time.Sleep(300 * time.Millisecond)

	body := scrape(t, registry, "secret-token")
	if !strings.Contains(body, "misty_test_flaky 7") {
		t.Fatalf("a failed sample overwrote the last good value:\n%s", body)
	}
	if !strings.Contains(body, "misty_metrics_sample_failures_total") {
		t.Fatal("sample failures are not counted, so staleness would be invisible")
	}
}
