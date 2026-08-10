package api

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	. "github.com/kannachi323/misty/server/internal/platform/httpapi"
)

// The callback is only ever loaded in a browser, so no outcome may answer with
// the JSON envelope the rest of the API uses.
func TestOAuthCallbackFailuresRenderHTMLRatherThanJSON(t *testing.T) {
	for _, failure := range []TestingOAuthCallbackFailure{
		TestingOAuthDeniedByProvider,
		TestingOAuthMalformedRedirect,
		TestingOAuthStaleState,
		TestingOAuthExchangeFailed,
		TestingOAuthTokenUnusable,
		TestingOAuthCloudConnectionLimit,
		TestingOAuthNotSaved,
	} {
		recorder := httptest.NewRecorder()
		TestingWriteOAuthCallbackFailure(recorder, "notion", failure, errors.New("cause"))

		if got := recorder.Header().Get("Content-Type"); got != "text/html; charset=utf-8" {
			t.Fatalf("%s Content-Type = %q, want text/html; charset=utf-8", failure.Reason, got)
		}
		if recorder.Code != failure.Status {
			t.Fatalf("%s status = %d, want %d", failure.Reason, recorder.Code, failure.Status)
		}
		body := recorder.Body.String()
		if !strings.HasPrefix(strings.TrimSpace(body), "<!doctype html>") {
			t.Fatalf("%s did not render a document: %s", failure.Reason, body)
		}
		// The reason is the whole point: a report of "it showed an error" has to
		// be matchable to one specific guard.
		if !strings.Contains(body, failure.Reason) {
			t.Fatalf("%s page omits its reason: %s", failure.Reason, body)
		}
		if !strings.Contains(body, "Notion") {
			t.Fatalf("%s page omits the provider name: %s", failure.Reason, body)
		}
	}
}

// A cancelled consent is an ordinary outcome, not a server fault, and must not
// be reported as one.
func TestDeniedConsentIsNotReportedAsAnError(t *testing.T) {
	recorder := httptest.NewRecorder()
	TestingWriteOAuthCallbackFailure(recorder, "google", TestingOAuthDeniedByProvider, nil)

	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", recorder.Code, http.StatusOK)
	}
}

// The provider segment comes from the URL, so an unknown value must still
// produce a readable page rather than reflecting the raw path segment.
func TestUnknownProviderStillRendersAReadablePage(t *testing.T) {
	recorder := httptest.NewRecorder()
	TestingWriteOAuthCallbackFailure(recorder, "<script>alert(1)</script>", TestingOAuthMalformedRedirect, nil)

	body := recorder.Body.String()
	if strings.Contains(body, "<script>alert") {
		t.Fatalf("page reflected the raw provider segment: %s", body)
	}
	if !strings.Contains(body, "The provider was not connected") {
		t.Fatalf("page missing generic provider wording: %s", body)
	}
}

func TestProviderRefusalDetailSummarisesBothQueryFields(t *testing.T) {
	request := httptest.NewRequest(
		http.MethodGet,
		"/api/oauth/providers/notion/callback?error=access_denied&error_description=User+said+no",
		nil,
	)
	if got, want := TestingProviderRefusalDetail(request), "access_denied: User said no"; got != want {
		t.Fatalf("TestingProviderRefusalDetail() = %q, want %q", got, want)
	}
}
