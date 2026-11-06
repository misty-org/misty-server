package api

import (
	api "github.com/misty-org/misty-server/internal/platform/httpapi"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestCookieCSRFProtection(t *testing.T) {
	handler := api.CookieCSRFProtection(func(origin string) bool { return origin == "https://misty.test" })(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusNoContent) }))
	for _, item := range []struct {
		name, path, origin, header string
		cookie, bearer             bool
		want                       int
	}{
		{"login missing header", "/v1/login", "https://misty.test", "", false, false, 403},
		{"Google begin missing header", "/v1/auth/google", "https://misty.test", "", false, false, 403},
		{"Google completion untrusted origin", "/v1/auth/google/complete", "https://evil.test", "1", false, false, 403},
		{"Google native completion", "/v1/auth/google/complete", "", "1", false, false, 204},
		{"login untrusted origin", "/api/login", "https://evil.test", "1", false, false, 403},
		{"cookie missing header", "/v1/spaces", "https://misty.test", "", true, false, 403},
		{"cookie trusted origin", "/v1/spaces", "https://misty.test", "1", true, false, 204},
		{"native explicit header", "/v1/login", "", "1", false, false, 204},
		{"webhook without cookies", "/webhook", "", "", false, false, 204},
		{"bearer without cookies", "/v1/auth/refresh", "", "", false, true, 204},
		{"bearer does not excuse a cookie", "/v1/spaces", "https://misty.test", "", true, true, 403},
		{"bearer does not excuse a sign-in", "/v1/login", "https://misty.test", "", false, true, 403},
	} {
		t.Run(item.name, func(t *testing.T) {
			r := httptest.NewRequest("POST", item.path, nil)
			r.Header.Set("Origin", item.origin)
			r.Header.Set("X-Misty-CSRF", item.header)
			if item.bearer {
				r.Header.Set("Authorization", "Bearer token")
			}
			if item.cookie {
				r.AddCookie(&http.Cookie{Name: api.SessionCookieName, Value: "session"})
			}
			w := httptest.NewRecorder()
			handler.ServeHTTP(w, r)
			if w.Code != item.want {
				t.Fatalf("status=%d want=%d", w.Code, item.want)
			}
		})
	}
}
