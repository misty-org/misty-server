package metrics

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
	. "github.com/kannachi323/misty/server/internal/platform/metrics"
)

func TestBodyBytesUpdateBeforeStreamCloses(t *testing.T) {
	registry := New()
	router := chi.NewRouter()
	router.Use(registry.Middleware)
	router.Post("/stream/{id}", func(w http.ResponseWriter, r *http.Request) {
		if _, err := io.ReadFull(r.Body, make([]byte, 3)); err != nil {
			t.Fatal(err)
		}
		_, _ = w.Write([]byte("data: first\n\n"))
		w.(http.Flusher).Flush()
		body := scrape(t, registry, "secret")
		for _, sample := range []string{
			`misty_http_body_bytes_total{direction="in",method="POST",route="/stream/{id}"} 3`,
			`misty_http_body_bytes_total{direction="out",method="POST",route="/stream/{id}"} 13`,
		} {
			if !strings.Contains(body, sample) {
				t.Fatalf("missing live metric %s", sample)
			}
		}
		if strings.Contains(body, "private-id") {
			t.Fatal("identifier leaked into labels")
		}
	})
	router.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest("POST", "/stream/private-id", strings.NewReader("abcdef")))
}

func TestSyncLabelsAreBoundedAndConnectionsAreBalanced(t *testing.T) {
	registry := New()
	handler := registry.Middleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		done := TrackSyncConnection(r.Context())
		RecordSyncMessage(r.Context(), "in", "heartbeat", 12)
		RecordSyncMessage(r.Context(), "out", "records", 50)
		RecordSyncMessage(r.Context(), "in", "user-controlled-private-id", 7)
		body := scrape(t, registry, "secret")
		if !strings.Contains(body, "misty_sync_connections 1") {
			t.Fatal("missing live connection")
		}
		done()
	}))
	handler.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest("GET", "/sync", nil))
	body := scrape(t, registry, "secret")
	for _, sample := range []string{
		"misty_sync_connections 0",
		`misty_sync_payload_bytes_total{direction="in",type="heartbeat"} 12`,
		`misty_sync_payload_bytes_total{direction="out",type="records"} 50`,
		`misty_sync_messages_total{direction="in",type="other"} 1`,
	} {
		if !strings.Contains(body, sample) {
			t.Fatalf("missing %s", sample)
		}
	}
	if strings.Contains(body, "private-id") {
		t.Fatal("unbounded type label")
	}
}
