package api

import (
	"html/template"
	"log"
	"net/http"
	"strings"
)

// The OAuth callback is the one endpoint in this API that is always loaded
// directly in a browser, by a redirect we do not control. Anything it returns
// is read by a person, not by client code — so every outcome renders HTML.
//
// It previously answered failures with the same JSON envelope as the rest of
// the API, which surfaced as a raw `{"code":"invalid_request"}` document at the
// end of an otherwise successful consent flow, with nothing to say which of the
// several guards had rejected the request.

type oauthCallbackView struct {
	Title    string
	Headline string
	Detail   string
	Hint     string
	Reason   string
	Success  bool
}

var oauthCallbackPage = template.Must(template.New("oauth-callback").Parse(`<!doctype html>
<html lang="en">
<head>
  <meta charset="utf-8">
  <meta name="viewport" content="width=device-width, initial-scale=1">
  <title>{{.Title}}</title>
  <style>
    :root { color-scheme: dark; font-family: Inter, ui-sans-serif, system-ui, -apple-system, BlinkMacSystemFont, "Segoe UI", sans-serif; background: #090b0f; color: #f4f7fb; }
    body { margin: 0; min-height: 100vh; display: grid; place-items: center; background: radial-gradient(circle at 50% 0%, #1b2635 0, #090b0f 42rem); }
    main { width: min(30rem, calc(100vw - 2rem)); padding: 2rem; border: 1px solid rgba(255,255,255,.12); border-radius: 1rem; background: rgba(15,18,24,.88); box-shadow: 0 24px 80px rgba(0,0,0,.36); text-align: center; }
    .mark { width: 3rem; height: 3rem; margin: 0 auto 1rem; display: grid; place-items: center; border-radius: 999px; color: white; font-size: 1.75rem; font-weight: 700; }
    .ok { background: #16a34a; }
    .bad { background: #b4453c; }
    h1 { margin: 0; font-size: 1.35rem; line-height: 1.2; }
    p { margin: .75rem 0 0; color: #aab4c3; line-height: 1.55; }
    strong { color: #f4f7fb; font-weight: 650; }
    code { font-family: ui-monospace, SFMono-Regular, Menlo, monospace; font-size: .8rem; color: #7f8b9c; }
    .reason { margin-top: 1.5rem; padding-top: 1rem; border-top: 1px solid rgba(255,255,255,.1); }
  </style>
</head>
<body>
  <main>
    <div class="mark {{if .Success}}ok{{else}}bad{{end}}">{{if .Success}}&check;{{else}}!{{end}}</div>
    <h1>{{.Headline}}</h1>
    <p>{{.Detail}}</p>
    {{if .Hint}}<p>{{.Hint}}</p>{{end}}
    {{if .Reason}}<p class="reason"><code>{{.Reason}}</code></p>{{end}}
  </main>
</body>
</html>`))

func writeOAuthCallbackPage(w http.ResponseWriter, status int, view oauthCallbackView) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Frame-Options", "DENY")
	w.WriteHeader(status)
	_ = oauthCallbackPage.Execute(w, view)
}

// TestingWriteProviderCompletionPage renders the shared success page for both
// the provider and cloud connection callbacks.
func TestingWriteProviderCompletionPage(w http.ResponseWriter, providerName, accountName string) {
	if strings.TrimSpace(accountName) == "" {
		accountName = providerName + " account"
	}
	writeOAuthCallbackPage(w, http.StatusOK, oauthCallbackView{
		Title:    providerName + " connected",
		Headline: providerName + " is connected",
		Detail:   "Misty saved " + accountName + ". Return to the Misty app to continue.",
		Hint:     "You can close this browser tab.",
		Success:  true,
	})
}

// TestingOAuthCallbackFailure names one specific way the callback can be rejected.
// The reason travels to the page so a failure can be reported precisely
// instead of as an anonymous bad request.
type TestingOAuthCallbackFailure struct {
	Reason string
	Detail string
	Hint   string
	Status int
}

var (
	// The provider itself refused, most often because the person pressed
	// "Cancel" on the consent screen.
	TestingOAuthDeniedByProvider = TestingOAuthCallbackFailure{
		Reason: "provider_denied",
		Detail: "The provider did not approve this connection.",
		Hint:   "Nothing was saved. You can close this tab and try connecting again.",
		Status: http.StatusOK,
	}
	// The redirect arrived without the parameters an OAuth callback must carry.
	TestingOAuthMalformedRedirect = TestingOAuthCallbackFailure{
		Reason: "malformed_redirect",
		Detail: "This callback link is missing the authorization code or state that the provider should have supplied.",
		Hint:   "Start the connection again from Misty rather than reloading this page.",
		Status: http.StatusBadRequest,
	}
	// The state token did not match a live, unused record. Expiry, replay
	// (including a browser reloading the callback URL) and tampering are
	// deliberately indistinguishable here.
	TestingOAuthStaleState = TestingOAuthCallbackFailure{
		Reason: "expired_or_used_link",
		Detail: "This connection link has already been used or has expired.",
		Hint:   "Connection links are valid for ten minutes and only once. Start the connection again from Misty.",
		Status: http.StatusBadRequest,
	}
	// We held a valid state but could not trade the code for a token.
	TestingOAuthExchangeFailed = TestingOAuthCallbackFailure{
		Reason: "token_exchange_failed",
		Detail: "Misty could not exchange the authorization code for an access token.",
		Hint:   "This usually means the provider credentials on the server are wrong or the redirect URL is not registered with the provider.",
		Status: http.StatusBadGateway,
	}
	// The exchange succeeded but the response was not usable.
	TestingOAuthTokenUnusable = TestingOAuthCallbackFailure{
		Reason: "token_unusable",
		Detail: "The provider returned a response Misty could not read as an access token.",
		Hint:   "Start the connection again. If it keeps happening the provider app configuration needs attention.",
		Status: http.StatusBadGateway,
	}
	// The authorization was fine; the account's plan does not allow another
	// cloud connection.
	TestingOAuthCloudConnectionLimit = TestingOAuthCallbackFailure{
		Reason: "cloud_connection_limit",
		Detail: "Basic accounts can connect one cloud account.",
		Hint:   "Disconnect the existing cloud account, or upgrade your plan, then try again.",
		Status: http.StatusForbidden,
	}
	// Everything upstream worked; persisting the connection did not.
	TestingOAuthNotSaved = TestingOAuthCallbackFailure{
		Reason: "not_saved",
		Detail: "Misty authorized the provider but could not save the connection to your Space.",
		Hint:   "Check that you still have permission to manage integrations in that Space, then try again.",
		Status: http.StatusInternalServerError,
	}
)

// TestingWriteOAuthCallbackFailure renders the failure page and records why, so a
// report of "it just showed an error" can be matched to a server log line.
func TestingWriteOAuthCallbackFailure(
	w http.ResponseWriter, provider string, failure TestingOAuthCallbackFailure, cause error,
) {
	if cause != nil {
		log.Printf(
			"OAuth callback rejected: provider=%s reason=%s error=%v",
			provider, failure.Reason, cause,
		)
	} else {
		log.Printf("OAuth callback rejected: provider=%s reason=%s", provider, failure.Reason)
	}
	name := providerDisplayName(provider)
	writeOAuthCallbackPage(w, failure.Status, oauthCallbackView{
		Title:    name + " was not connected",
		Headline: name + " was not connected",
		Detail:   failure.Detail,
		Hint:     failure.Hint,
		Reason:   failure.Reason,
	})
}

// TestingProviderRefusalDetail summarises what the provider said when it refused, for
// the log line only — the values come from the redirect and are never rendered.
func TestingProviderRefusalDetail(r *http.Request) string {
	query := r.URL.Query()
	detail := query.Get("error")
	if description := query.Get("error_description"); description != "" {
		detail += ": " + description
	}
	return detail
}

// providerDisplayName prefers the catalog's branded name and falls back to the
// raw identifier, which may be anything at all since it comes from the URL.
func providerDisplayName(provider string) string {
	if definition, ok := TestingProviderOAuthCatalog[provider]; ok {
		return definition.Name
	}
	if definition, ok := TestingCloudOAuthCatalog[provider]; ok {
		return definition.Name
	}
	if definition, ok := TestingConnectedAccountOAuthCatalog[provider]; ok {
		return definition.Name
	}
	if strings.TrimSpace(provider) == "" {
		return "The provider"
	}
	return "The provider"
}
