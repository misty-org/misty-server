package api

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	. "github.com/kannachi323/misty/server/internal/platform/httpapi"
)

func TestLibraryPreviewCacheHeaders(t *testing.T) {
	sha := strings.Repeat("a", 64)
	tests := []struct {
		name         string
		url          string
		ifNoneMatch  string
		cacheControl string
		notModified  bool
	}{
		{name: "versioned immutable", url: "/preview?cache_version=7", cacheControl: "private, max-age=31536000, immutable"},
		{name: "unversioned revalidates", url: "/preview", cacheControl: "private, no-cache"},
		{name: "matching validator", url: "/preview", ifNoneMatch: `W/"other", "` + sha + `"`, cacheControl: "private, no-cache", notModified: true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			request := httptest.NewRequest(http.MethodGet, test.url, nil)
			request.Header.Set("If-None-Match", test.ifNoneMatch)
			response := httptest.NewRecorder()
			response.Header().Set("Vary", "Origin")
			notModified := TestingWriteLibraryPreviewCacheHeaders(response, request, sha)
			if notModified != test.notModified {
				t.Fatalf("notModified = %v, want %v", notModified, test.notModified)
			}
			if got := response.Header().Get("Cache-Control"); got != test.cacheControl {
				t.Fatalf("Cache-Control = %q, want %q", got, test.cacheControl)
			}
			if got := response.Header().Get("ETag"); got != `"`+sha+`"` {
				t.Fatalf("ETag = %q", got)
			}
			vary := strings.Join(response.Header().Values("Vary"), ", ")
			if !strings.Contains(vary, "Origin") || !strings.Contains(vary, "Authorization") || !strings.Contains(vary, "X-Misty-Library-Reauthentication") {
				t.Fatalf("Vary = %q", vary)
			}
			if test.notModified && response.Code != http.StatusNotModified {
				t.Fatalf("status = %d, want %d", response.Code, http.StatusNotModified)
			}
		})
	}
}
