package console

import (
	"crypto/subtle"
	"net"
	"net/http"
	"sync"

	"github.com/kannachi323/misty/server/internal/platform/security"
)

const sessionCookie = "misty_console_session"

// session is one browser that exchanged the launch token for a cookie.
type session struct {
	csrf string
}

// authenticator exchanges the one-time launch token printed at startup for a
// session cookie. Only hashes are kept in memory, and restarting the console
// invalidates every session.
type authenticator struct {
	launchHash string
	mu         sync.Mutex
	sessions   map[string]session
}

func newAuthenticator(launchToken string) *authenticator {
	return &authenticator{
		launchHash: security.HashToken(launchToken),
		sessions:   map[string]session{},
	}
}

func (a *authenticator) launchTokenValid(token string) bool {
	if token == "" {
		return false
	}
	return subtle.ConstantTimeCompare([]byte(security.HashToken(token)), []byte(a.launchHash)) == 1
}

// startSession sets a fresh session cookie. The launch token stays valid so
// `misty server up --gui` can reopen the page in another browser window.
func (a *authenticator) startSession(w http.ResponseWriter) error {
	id, err := security.GenerateSecureToken()
	if err != nil {
		return err
	}
	csrf, err := security.GenerateSecureToken()
	if err != nil {
		return err
	}
	a.mu.Lock()
	a.sessions[security.HashToken(id)] = session{csrf: csrf}
	a.mu.Unlock()
	http.SetCookie(w, &http.Cookie{
		Name:     sessionCookie,
		Value:    id,
		Path:     "/",
		HttpOnly: true,
		SameSite: http.SameSiteStrictMode,
	})
	return nil
}

func (a *authenticator) sessionFor(r *http.Request) (session, bool) {
	cookie, err := r.Cookie(sessionCookie)
	if err != nil || cookie.Value == "" {
		return session{}, false
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	s, ok := a.sessions[security.HashToken(cookie.Value)]
	return s, ok
}

// requireHost blocks DNS-rebinding: a hostile page that points its own
// hostname at 127.0.0.1 still sends that hostname in the Host header.
func requireHost(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		host, _, err := net.SplitHostPort(r.Host)
		if err != nil {
			host = r.Host
		}
		ip := net.ParseIP(host)
		if host != "localhost" && (ip == nil || !ip.IsLoopback()) {
			http.Error(w, "forbidden host", http.StatusForbidden)
			return
		}
		next.ServeHTTP(w, r)
	})
}

// requireSession redirects a valid ?token= launch link to a cookie session
// and rejects everything else without one. Mutating requests must also carry
// the session's CSRF token, which htmx sends from the page's hx-headers.
func (a *authenticator) requireSession(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if token := r.URL.Query().Get("token"); token != "" {
			if !a.launchTokenValid(token) {
				renderUnauthorized(w, r)
				return
			}
			if err := a.startSession(w); err != nil {
				http.Error(w, "could not start a session", http.StatusInternalServerError)
				return
			}
			clean := *r.URL
			query := clean.Query()
			query.Del("token")
			clean.RawQuery = query.Encode()
			http.Redirect(w, r, clean.RequestURI(), http.StatusSeeOther)
			return
		}
		s, ok := a.sessionFor(r)
		if !ok {
			renderUnauthorized(w, r)
			return
		}
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			provided := r.Header.Get("X-CSRF-Token")
			if r.Header.Get("HX-Request") != "true" ||
				subtle.ConstantTimeCompare([]byte(provided), []byte(s.csrf)) != 1 {
				http.Error(w, "missing or invalid CSRF token", http.StatusForbidden)
				return
			}
		}
		next.ServeHTTP(w, r.WithContext(withSession(r.Context(), s)))
	})
}

// securityHeaders keeps the console same-origin only: no framing, no
// third-party scripts, and no inline script execution.
func securityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := w.Header()
		h.Set("Content-Security-Policy", "default-src 'self'; img-src 'self' data:; frame-ancestors 'none'; base-uri 'none'; form-action 'self'")
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("Referrer-Policy", "no-referrer")
		h.Set("Cache-Control", "no-store")
		next.ServeHTTP(w, r)
	})
}
