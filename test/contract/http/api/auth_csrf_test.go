package api

import (
	api "github.com/kannachi323/misty/server/internal/platform/httpapi"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestCookieCSRFProtection(t *testing.T) {
	handler := api.CookieCSRFProtection(func(origin string) bool { return origin == "https://misty.test" })(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusNoContent) }))
	for _, item := range []struct {
		name, path, origin, header string
		cookie                     bool
		want                       int
	}{
		{"login missing header", "/v1/login", "https://misty.test", "", false, 403},
		{"login untrusted origin", "/api/login", "https://evil.test", "1", false, 403},
		{"cookie missing header", "/v1/spaces", "https://misty.test", "", true, 403},
		{"cookie trusted origin", "/v1/spaces", "https://misty.test", "1", true, 204},
		{"native explicit header", "/v1/login", "", "1", false, 204},
		{"webhook without cookies", "/stripe/webhook", "", "", false, 204},
	} {
		t.Run(item.name, func(t *testing.T) {
			r := httptest.NewRequest("POST", item.path, nil)
			r.Header.Set("Origin", item.origin)
			r.Header.Set("X-Misty-CSRF", item.header)
			if item.cookie {
				r.AddCookie(&http.Cookie{Name: api.TestingSessionCookieName, Value: "session"})
			}
			w := httptest.NewRecorder()
			handler.ServeHTTP(w, r)
			if w.Code != item.want {
				t.Fatalf("status=%d want=%d", w.Code, item.want)
			}
		})
	}
}
