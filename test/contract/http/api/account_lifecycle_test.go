package api

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"

	. "github.com/kannachi323/misty/server/internal/platform/httpapi"
)

func TestPostProviderRevocationUsesFormOrBearerWithoutLeakingTokens(t *testing.T) {
	var formBody, authorization string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		formBody = string(raw)
		authorization = r.Header.Get("Authorization")
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	if err := TestingPostProviderRevocation(
		context.Background(), server.URL,
		url.Values{"token": {"refresh-secret"}}, "",
	); err != nil {
		t.Fatal(err)
	}
	if formBody != "token=refresh-secret" || authorization != "" {
		t.Fatalf("form revocation = body:%q auth:%q", formBody, authorization)
	}
	if err := TestingPostProviderRevocation(
		context.Background(), server.URL, nil, "access-secret",
	); err != nil {
		t.Fatal(err)
	}
	if formBody != "" || authorization != "Bearer access-secret" {
		t.Fatalf("bearer revocation = body:%q auth:%q", formBody, authorization)
	}
}

func TestPostProviderRevocationFailsClosedOnProviderError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusTooManyRequests)
	}))
	defer server.Close()
	if err := TestingPostProviderRevocation(
		context.Background(), server.URL, nil, "access-secret",
	); err == nil {
		t.Fatal("provider failure was treated as successful revocation")
	}
}
