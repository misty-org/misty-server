package app

import (
	"bytes"
	"log"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	. "github.com/kannachi323/misty/server/internal/app"

	"github.com/go-chi/chi/v5"
)

func TestRequestObservabilityUsesSafeCallerCorrelationID(t *testing.T) {
	var logs bytes.Buffer
	previous := log.Writer()
	log.SetOutput(&logs)
	t.Cleanup(func() { log.SetOutput(previous) })

	router := chi.NewRouter()
	router.Use(TestingRequestObservabilityMiddleware)
	router.Get("/spaces/{spaceID}", func(w http.ResponseWriter, r *http.Request) {
		if got := TestingRequestIDFromContext(r.Context()); got != "client-request-123" {
			t.Fatalf("context request id = %q", got)
		}
		w.WriteHeader(http.StatusNoContent)
	})
	request := httptest.NewRequest(http.MethodGet, "/spaces/space_secret?ticket=secret", nil)
	request.Header.Set("X-Request-ID", "client-request-123")
	response := httptest.NewRecorder()

	router.ServeHTTP(response, request)

	if got := response.Header().Get("X-Request-ID"); got != "client-request-123" {
		t.Fatalf("response request id = %q", got)
	}
	output := logs.String()
	for _, expected := range []string{
		`"event":"http_request_completed"`,
		`"request_id":"client-request-123"`,
		`"route":"/spaces/{spaceID}"`,
		`"status":204`,
	} {
		if !strings.Contains(output, expected) {
			t.Fatalf("structured log missing %s: %s", expected, output)
		}
	}
	for _, forbidden := range []string{"space_secret", "ticket", "secret"} {
		if strings.Contains(output, forbidden) {
			t.Fatalf("structured log leaked %q: %s", forbidden, output)
		}
	}
}

func TestRequestObservabilityReplacesUnsafeCorrelationID(t *testing.T) {
	handler := TestingRequestObservabilityMiddleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := TestingRequestIDFromContext(r.Context()); !strings.HasPrefix(got, "req_") {
			t.Fatalf("generated request id = %q", got)
		}
	}))
	request := httptest.NewRequest(http.MethodGet, "/", nil)
	request.Header.Set("X-Request-ID", "contains user@email.test")
	response := httptest.NewRecorder()

	handler.ServeHTTP(response, request)

	if got := response.Header().Get("X-Request-ID"); !strings.HasPrefix(got, "req_") {
		t.Fatalf("response request id = %q", got)
	}
}

func TestRequestObservabilitySuppressesRoutineMePolling(t *testing.T) {
	for _, status := range []int{http.StatusOK, http.StatusUnauthorized} {
		t.Run(http.StatusText(status), func(t *testing.T) {
			var logs bytes.Buffer
			previous := log.Writer()
			log.SetOutput(&logs)
			t.Cleanup(func() { log.SetOutput(previous) })

			router := chi.NewRouter()
			router.Use(TestingRequestObservabilityMiddleware)
			router.Get("/api/me", func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(status)
			})

			router.ServeHTTP(
				httptest.NewRecorder(),
				httptest.NewRequest(http.MethodGet, "/api/me", nil),
			)

			if output := logs.String(); output != "" {
				t.Fatalf("routine /api/me request was logged: %s", output)
			}
		})
	}
}

func TestRequestObservabilityStillLogsMeServerFailures(t *testing.T) {
	var logs bytes.Buffer
	previous := log.Writer()
	log.SetOutput(&logs)
	t.Cleanup(func() { log.SetOutput(previous) })

	router := chi.NewRouter()
	router.Use(TestingRequestObservabilityMiddleware)
	router.Get("/api/me", func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "failed", http.StatusInternalServerError)
	})

	router.ServeHTTP(
		httptest.NewRecorder(),
		httptest.NewRequest(http.MethodGet, "/api/me", nil),
	)

	output := logs.String()
	if !strings.Contains(output, `"route":"/api/me"`) ||
		!strings.Contains(output, `"status":500`) {
		t.Fatalf("/api/me server failure was not logged: %s", output)
	}
}
