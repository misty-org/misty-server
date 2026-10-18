package api

import (
	"net/http/httptest"
	"testing"
)

func TestOutsideAgentReason(t *testing.T) {
	for _, tc := range []struct {
		name    string
		headers map[string]string
		want    string
	}{
		{"the Misty app", map[string]string{"User-Agent": "MistyDesktop/0.4.0 (macos)", "X-Misty-CSRF": "1"}, ""},
		{"no user agent", map[string]string{"User-Agent": "", "X-Misty-CSRF": "1"}, "no_user_agent"},
		{"curl", map[string]string{"User-Agent": "curl/8.7.1", "X-Misty-CSRF": "1"}, "automation_user_agent"},
		{"python", map[string]string{"User-Agent": "python-requests/2.32", "X-Misty-CSRF": "1"}, "automation_user_agent"},
		{"an AI agent", map[string]string{"User-Agent": "Claude-User/1.0", "X-Misty-CSRF": "1"}, "automation_user_agent"},
		{"an MCP client", map[string]string{"User-Agent": "mcp-client/1.2", "X-Misty-CSRF": "1"}, "automation_user_agent"},
		{"a browser", map[string]string{"User-Agent": "Mozilla/5.0 (Macintosh) Safari/605.1.15", "X-Misty-CSRF": "1"}, "not_misty_app"},
		{"the app's name with a library behind it", map[string]string{"User-Agent": "MistyDesktop/0.4.0 python-requests/2.32", "X-Misty-CSRF": "1"}, "not_misty_app"},
		{"no app header", map[string]string{"User-Agent": "MistyDesktop/0.4.0 (macos)"}, "missing_app_header"},
		{"a web page", map[string]string{"User-Agent": "MistyDesktop/0.4.0 (macos)", "X-Misty-CSRF": "1", "Origin": "https://example.com"}, "browser_page"},
		{"WebDriver in the app", map[string]string{"User-Agent": "MistyDesktop/0.4.0 (macos)", "X-Misty-CSRF": "1", "X-Misty-Automation": "webdriver"}, "app_automation_webdriver"},
		{"an odd automation report", map[string]string{"User-Agent": "MistyDesktop/0.4.0 (macos)", "X-Misty-CSRF": "1", "X-Misty-Automation": "<script>"}, "app_automation_reported"},
	} {
		r := httptest.NewRequest("POST", "/v1/me/coding/run/lease", nil)
		for name, value := range tc.headers {
			r.Header.Set(name, value)
		}
		if got := outsideAgentReason(r); got != tc.want {
			t.Errorf("%s: reason = %q, want %q", tc.name, got, tc.want)
		}
	}
}
