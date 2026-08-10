package integration

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	db "github.com/kannachi323/misty/server/internal/platform/postgres"
	"github.com/kannachi323/misty/server/test/testkit"
)

const sessionCookieName = "misty_session"

func openIntegrationDatabase(t *testing.T) *db.Database {
	t.Helper()
	return testkit.OpenDatabase(t)
}

func performJSONRequest(t *testing.T, handler http.HandlerFunc, method string, path string, body any, cookies ...*http.Cookie) *httptest.ResponseRecorder {
	t.Helper()

	var payload bytes.Buffer
	if body != nil {
		if err := json.NewEncoder(&payload).Encode(body); err != nil {
			t.Fatalf("json.NewEncoder() error = %v", err)
		}
	}

	req := httptest.NewRequest(method, path, &payload)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	for _, cookie := range cookies {
		req.AddCookie(cookie)
	}

	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	return rec
}

func performBearerJSONRequest(t *testing.T, handler http.HandlerFunc, method string, path string, body any, token string) *httptest.ResponseRecorder {
	t.Helper()

	var payload bytes.Buffer
	if body != nil {
		if err := json.NewEncoder(&payload).Encode(body); err != nil {
			t.Fatalf("json.NewEncoder() error = %v", err)
		}
	}

	req := httptest.NewRequest(method, path, &payload)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	req.Header.Set("Authorization", "Bearer "+token)

	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	return rec
}

func decodeJSONResponse(t *testing.T, rec *httptest.ResponseRecorder) map[string]any {
	t.Helper()

	var payload map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &payload); err != nil {
		t.Fatalf("json.Unmarshal() error = %v; body = %q", err, rec.Body.String())
	}
	return payload
}

func requireCookie(t *testing.T, rec *httptest.ResponseRecorder, name string) *http.Cookie {
	t.Helper()

	resp := rec.Result()
	defer resp.Body.Close()

	for _, cookie := range resp.Cookies() {
		if cookie.Name == name {
			return cookie
		}
	}

	t.Fatalf("cookie %q not found", name)
	return nil
}

func countStripePurchases(t *testing.T, database *db.Database, checkoutSessionID string) int {
	t.Helper()

	var count int
	err := database.Conn.QueryRow(
		`SELECT COUNT(*) FROM stripe_purchases WHERE stripe_checkout_session_id = $1`,
		checkoutSessionID,
	).Scan(&count)
	if err != nil {
		t.Fatalf("failed to count stripe purchases: %v", err)
	}
	return count
}

func totalStripePurchases(t *testing.T, database *db.Database) int {
	t.Helper()

	var count int
	err := database.Conn.QueryRow(`SELECT COUNT(*) FROM stripe_purchases`).Scan(&count)
	if err != nil {
		t.Fatalf("failed to count stripe purchases: %v", err)
	}
	return count
}
