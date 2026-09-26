package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/kannachi323/misty/server/internal/billingadapter"
	. "github.com/kannachi323/misty/server/internal/platform/httpapi"
)

type customerAdapter struct {
	request billingadapter.Request
	action  string
	err     error
	summary json.RawMessage
}

func (*customerAdapter) Enabled() bool { return true }
func (a *customerAdapter) Do(_ context.Context, action string, r billingadapter.Request) (billingadapter.Decision, error) {
	a.request = r
	a.action = action
	return billingadapter.Decision{Allowed: true, Summary: a.summary}, a.err
}
func TestBillingAdapterCustomerIdentityAndOutage(t *testing.T) {
	database := openPresenceTestDatabase(t)
	user, err := database.CreateUser("Adapter Customer", uniqueTestEmail("billing-adapter"), "password123")
	if err != nil {
		t.Fatal(err)
	}
	token := newConversationTestBearerToken(t, database, user.ID)
	adapter := &customerAdapter{summary: json.RawMessage(`{"url":"https://billing.example/checkout"}`)}
	database.Billing = &billingadapter.Service{Adapter: adapter}
	request := httptest.NewRequest("POST", "/billing/checkout-session", strings.NewReader(`{"tier":"custom-plan","interval":"month"}`))
	request.AddCookie(&http.Cookie{Name: TestingSessionCookieName, Value: token})
	request.Header.Set("Idempotency-Key", "customer-click")
	response := httptest.NewRecorder()
	CreateCheckoutSession(database).ServeHTTP(response, request)
	if response.Code != 200 || adapter.action != "checkout" || adapter.request.AccountID != user.ID || adapter.request.Key != "customer-click" || adapter.request.Customer.Email != user.Email || adapter.request.Selection.Product != "custom-plan" {
		t.Fatalf("status %d, request %#v", response.Code, adapter.request)
	}
	// A billing outage cannot stop the account/browser from loading.
	adapter.err = billingadapter.ErrUnavailable
	request = httptest.NewRequest("GET", "/me", nil)
	request.AddCookie(&http.Cookie{Name: TestingSessionCookieName, Value: token})
	response = httptest.NewRecorder()
	GetMe(database).ServeHTTP(response, request)
	if response.Code != 200 || !strings.Contains(response.Body.String(), `"allows_use":true`) || !strings.Contains(response.Body.String(), `"kind":"unavailable"`) {
		t.Fatalf("account blocked by billing outage: %d %s", response.Code, response.Body.String())
	}
	database.Billing = nil
	response = httptest.NewRecorder()
	GetMe(database).ServeHTTP(response, request)
	if response.Code != 200 || !strings.Contains(response.Body.String(), `"kind":"disabled"`) {
		t.Fatalf("disabled adapter: %d %s", response.Code, response.Body.String())
	}
}
