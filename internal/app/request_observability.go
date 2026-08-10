package app

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"log"
	"net/http"
	"regexp"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
)

type requestIDContextKey struct{}

var safeRequestID = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._:-]{7,127}$`)

// requestObservabilityMiddleware gives every API request a correlation id and
// emits one bounded, structured completion event. It deliberately logs the
// route template rather than the raw URL so Space ids, filenames, query values,
// and signed credentials never enter process logs.
func TestingRequestObservabilityMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requestID := r.Header.Get("X-Request-ID")
		if !safeRequestID.MatchString(requestID) {
			requestID = newRequestID()
		}
		r.Header.Set("X-Request-ID", requestID)
		w.Header().Set("X-Request-ID", requestID)
		ctx := context.WithValue(r.Context(), requestIDContextKey{}, requestID)
		r = r.WithContext(ctx)

		wrapped := middleware.NewWrapResponseWriter(w, r.ProtoMajor)
		started := time.Now()
		next.ServeHTTP(wrapped, r)

		route := requestRoutePattern(r)
		status := responseStatus(wrapped.Status())
		if !shouldLogRequest(r.Method, route, status) {
			return
		}
		entry, _ := json.Marshal(map[string]any{
			"duration_ms": time.Since(started).Milliseconds(),
			"event":       "http_request_completed",
			"level":       requestLogLevel(status),
			"method":      r.Method,
			"request_id":  requestID,
			"route":       route,
			"status":      status,
		})
		log.Print(string(entry))
	})
}

func TestingRequestIDFromContext(ctx context.Context) string {
	requestID, _ := ctx.Value(requestIDContextKey{}).(string)
	return requestID
}

func newRequestID() string {
	var value [16]byte
	if _, err := rand.Read(value[:]); err == nil {
		return "req_" + hex.EncodeToString(value[:])
	}
	return "req_fallback_" + time.Now().UTC().Format("20060102T150405.000000000")
}

func requestRoutePattern(r *http.Request) string {
	if routeContext := chi.RouteContext(r.Context()); routeContext != nil {
		if pattern := routeContext.RoutePattern(); pattern != "" {
			return pattern
		}
	}
	return "unmatched"
}

func responseStatus(status int) int {
	if status == 0 {
		return http.StatusOK
	}
	return status
}

func requestLogLevel(status int) string {
	switch {
	case responseStatus(status) >= 500:
		return "error"
	case responseStatus(status) >= 400:
		return "warn"
	default:
		return "info"
	}
}

// /api/me is polled to keep the client authentication state synchronized.
// Successful checks and expected post-logout 401s are routine, high-volume
// traffic. Server failures remain visible.
func shouldLogRequest(method, route string, status int) bool {
	return method != http.MethodGet ||
		route != "/api/me" ||
		responseStatus(status) >= http.StatusInternalServerError
}
