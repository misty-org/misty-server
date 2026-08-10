package api

import (
	"errors"
	"net/http"
	"net/url"
	"strings"
	"time"

	db "github.com/kannachi323/misty/server/internal/platform/postgres"
	"github.com/kannachi323/misty/server/internal/platform/security"
)

// AuthHandoffService signs a desktop user into the website. The desktop app
// holds a bearer token in the OS keychain; the system browser has its own
// cookie jar, so without a handoff every "Account settings" click would land on
// a sign-in wall.
//
// The shape mirrors PasswordResetService.Start: the single-use token is moved
// out of the URL and into an HttpOnly cookie before the redirect, so it never
// reaches the SPA's address bar, history, or Referer header.
type AuthHandoffService struct {
	database   *db.Database
	startURL   string
	websiteURL string
	now        func() time.Time
}

// defaultHandoffPath is where a handoff lands when the caller names no path.
const defaultHandoffPath = "/settings"

// handoffPathAllowlist bounds the redirect to account surfaces. An open
// redirect here would turn a trusted API origin into a phishing hop, so the set
// is explicit rather than pattern-matched.
var handoffPathAllowlist = map[string]bool{
	"/settings":         true,
	"/settings/account": true,
	"/settings/usage":   true,
	"/settings/billing": true,
	"/settings/privacy": true,
}

func NewAuthHandoffService(database *db.Database, startURL, websiteURL string) (*AuthHandoffService, error) {
	if database == nil {
		return nil, errors.New("auth handoff database is required")
	}
	if err := TestingValidateResetURL(startURL); err != nil {
		return nil, err
	}
	if err := TestingValidateResetURL(websiteURL); err != nil {
		return nil, err
	}

	return &AuthHandoffService{
		database:   database,
		startURL:   startURL,
		websiteURL: strings.TrimSuffix(websiteURL, "/"),
		now:        time.Now,
	}, nil
}

// Mint is called by the desktop app with its bearer token. It returns a URL on
// the API origin that the app opens in the system browser.
func (s *AuthHandoffService) Mint() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		userID, err := sessionUserID(r, s.database)
		if err != nil {
			http.Error(w, "internal error", http.StatusInternalServerError)
			return
		}
		if userID == "" {
			http.Error(w, "not authenticated", http.StatusUnauthorized)
			return
		}

		var body struct {
			Path string `json:"path"`
		}
		// An absent or malformed body is not an error: the default path covers
		// the common "open my account settings" case.
		_ = decodeJSON(w, r, &body)

		redirectPath := TestingNormalizeHandoffPath(body.Path)
		if redirectPath == "" {
			http.Error(w, "unsupported handoff path", http.StatusBadRequest)
			return
		}

		rawToken, err := security.GenerateSecureToken()
		if err != nil {
			http.Error(w, "internal error", http.StatusInternalServerError)
			return
		}

		expiresAt := s.now().Add(db.AuthHandoffTTL)
		if err := s.database.CreateAuthHandoffToken(userID, security.HashToken(rawToken), redirectPath, expiresAt); err != nil {
			http.Error(w, "internal error", http.StatusInternalServerError)
			return
		}

		handoffURL, err := s.buildStartLink(rawToken)
		if err != nil {
			http.Error(w, "internal error", http.StatusInternalServerError)
			return
		}

		writeJSON(w, http.StatusOK, map[string]string{"url": handoffURL})
	}
}

// Start is opened by the browser. It burns the token, mints a short-lived
// session cookie, and redirects into the SPA.
func (s *AuthHandoffService) Start() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		token := strings.TrimSpace(r.URL.Query().Get("token"))
		if token == "" {
			http.Redirect(w, r, s.websiteURL+defaultHandoffPath, http.StatusSeeOther)
			return
		}

		userID, redirectPath, err := s.database.ConsumeAuthHandoffToken(security.HashToken(token), s.now())
		switch {
		case errors.Is(err, db.ErrAuthHandoffTokenInvalid):
			// The SPA will bounce an unauthenticated visitor to sign-in, which
			// is the right destination for a stale or replayed link.
			http.Redirect(w, r, s.websiteURL+defaultHandoffPath, http.StatusSeeOther)
			return
		case err != nil:
			http.Error(w, "internal error", http.StatusInternalServerError)
			return
		}

		sessionToken, err := security.GenerateSecureToken()
		if err != nil {
			http.Error(w, "internal error", http.StatusInternalServerError)
			return
		}
		if err := s.database.CreateSessionWithTTL(security.HashToken(sessionToken), userID, db.AuthHandoffSessionTTL); err != nil {
			http.Error(w, "internal error", http.StatusInternalServerError)
			return
		}

		writeSessionCookie(w, r, sessionToken, db.AuthHandoffSessionTTL)

		// Re-validate on the way out: the stored path came from the allowlist,
		// but a bad row must never become an open redirect.
		if TestingNormalizeHandoffPath(redirectPath) == "" {
			redirectPath = defaultHandoffPath
		}
		http.Redirect(w, r, s.websiteURL+redirectPath, http.StatusSeeOther)
	}
}

func (s *AuthHandoffService) buildStartLink(token string) (string, error) {
	startURL, err := url.Parse(s.startURL)
	if err != nil {
		return "", err
	}

	query := startURL.Query()
	query.Set("token", token)
	startURL.RawQuery = query.Encode()
	return startURL.String(), nil
}

// TestingNormalizeHandoffPath returns the allowlisted path, or "" when the
// caller asked for something that is not an account surface.
func TestingNormalizeHandoffPath(rawPath string) string {
	trimmed := strings.TrimSpace(rawPath)
	if trimmed == "" {
		return defaultHandoffPath
	}
	// Reject anything that could escape the site: absolute URLs, scheme-relative
	// "//evil.com", and traversal.
	if !strings.HasPrefix(trimmed, "/") || strings.HasPrefix(trimmed, "//") || strings.Contains(trimmed, "..") {
		return ""
	}
	trimmed = strings.TrimSuffix(trimmed, "/")
	if trimmed == "" {
		trimmed = defaultHandoffPath
	}
	if !handoffPathAllowlist[trimmed] {
		return ""
	}
	return trimmed
}
