package metrics

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
	. "github.com/misty-org/misty-server/internal/platform/metrics"
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
