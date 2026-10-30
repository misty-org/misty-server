package api

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"errors"
	"fmt"
	"html"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/kannachi323/misty/server/internal/platform/db"
	"github.com/kannachi323/misty/server/internal/platform/security"
	"google.golang.org/api/idtoken"
)

// The completion code binds a finished Google sign-in to the browser that
// finished it. Misty receives it through its misty:// link on this computer,
// or the person types it. Ambiguous characters are left out of the alphabet.
const googleCompletionAlphabet = "ABCDEFGHJKMNPQRSTVWXYZ23456789"

const googleCompletionLength = 10

func newGoogleCompletionCode() (string, error) {
	code := make([]byte, 0, googleCompletionLength)
	buffer := make([]byte, 32)
	limit := byte(256 - 256%len(googleCompletionAlphabet))
	for len(code) < googleCompletionLength {
		if _, err := rand.Read(buffer); err != nil {
			return "", err
		}
		for _, value := range buffer {
			if value < limit && len(code) < googleCompletionLength {
				code = append(code, googleCompletionAlphabet[int(value)%len(googleCompletionAlphabet)])
			}
		}
	}
	return string(code), nil
}

// normalizeGoogleCompletionCode accepts the code as displayed (with a dash)
// or typed in lower case. Anything else yields "".
func normalizeGoogleCompletionCode(raw string) string {
	var code strings.Builder
	for _, character := range strings.ToUpper(raw) {
		switch {
		case character == '-' || character == ' ':
			continue
		case strings.ContainsRune(googleCompletionAlphabet, character):
			code.WriteRune(character)
		default:
			return ""
		}
	}
	if code.Len() != googleCompletionLength {
		return ""
	}
	return code.String()
}

func validGoogleIdentity(p *idtoken.Payload, nonce, clientID string) bool {
	if p == nil || p.Subject == "" || (p.Issuer != "https://accounts.google.com" && p.Issuer != "accounts.google.com") || p.Audience != clientID || p.Expires <= time.Now().Unix() {
		return false
	}
	email, _ := p.Claims["email"].(string)
	verified, _ := p.Claims["email_verified"].(bool)
	gotNonce, _ := p.Claims["nonce"].(string)
	azp, _ := p.Claims["azp"].(string)
	return verified && strings.TrimSpace(email) != "" && nonce != "" && subtle.ConstantTimeCompare([]byte(gotNonce), []byte(nonce)) == 1 && (azp == "" || azp == clientID)
}

func (s *GoogleSignInService) Complete() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !s.enabled(w) {
			return
		}
		var body struct {
			Token          string `json:"flow_token"`
			CompletionCode string `json:"completion_code"`
		}
		if decodeJSON(w, r, &body) != nil {
			return
		}
		if body.Token == "" || len(body.Token) > 256 || len(body.CompletionCode) > 64 {
			http.Error(w, "invalid sign-in token", 400)
			return
		}
		completionHash := ""
		if strings.TrimSpace(body.CompletionCode) != "" {
			code := normalizeGoogleCompletionCode(body.CompletionCode)
			if code == "" {
				http.Error(w, "That code is not valid. Check the code shown in your browser.", http.StatusBadRequest)
				return
			}
			completionHash = security.HashToken(code)
		}
		flow, err := s.store.ConsumeGoogleSignInFlow(r.Context(), security.HashToken(body.Token), completionHash)
		if errors.Is(err, db.ErrGoogleFlowNeedsCode) {
			writeJSON(w, http.StatusAccepted, map[string]string{"status": "confirm"})
			return
		}
		if errors.Is(err, db.ErrGoogleFlowInvalid) {
			http.Error(w, err.Error(), http.StatusGone)
			return
		}
		if err != nil {
			http.Error(w, "internal error", 500)
			return
		}
		if flow == nil {
			writeJSON(w, http.StatusAccepted, map[string]string{"status": "pending"})
			return
		}
		if flow.ErrorCode == "registration_closed" {
			writeRegistrationClosed(w)
			return
		}
		if flow.ErrorCode != "" {
			http.Error(w, googleSignInError(flow.ErrorCode), http.StatusConflict)
			return
		}
		user, err := s.store.GetUserByID(flow.UserID)
		if err != nil || user == nil || user.Provider != "google" {
			http.Error(w, "Google sign-in is no longer available for this account", http.StatusUnauthorized)
			return
		}
		if flow.ReauthenticateUserID != "" {
			if user.ID != flow.ReauthenticateUserID {
				http.Error(w, "Google account does not match", http.StatusForbidden)
				return
			}
			token, err := security.GenerateSecureToken()
			if err != nil {
				http.Error(w, "internal error", 500)
				return
			}
			if err = s.store.CreateGoogleReauthenticationToken(r.Context(), user.ID, security.HashToken(token)); err != nil {
				http.Error(w, "internal error", 500)
				return
			}
			writeJSON(w, http.StatusOK, map[string]string{"reauthentication_token": token})
			return
		}
		s.verification.AfterSignup(r, user, true)
		s.session(w, r, user)
	}
}

func (s *GoogleSignInService) googleAccount(flow db.GoogleSignInFlow, payload *idtoken.Payload, email string) (*db.User, error) {
	if flow.ReauthenticateUserID == "" {
		// A server closed to new accounts still signs existing ones in.
		if gate, ok := s.store.(interface {
			accountCounter
			GetUserByEmail(string) (*db.User, string, error)
		}); ok {
			if existing, _, err := gate.GetUserByEmail(email); err != nil {
				return nil, err
			} else if existing == nil {
				allowed, err := registrationAllowed(context.Background(), gate)
				if err != nil {
					return nil, err
				}
				if !allowed {
					return nil, ErrRegistrationClosed
				}
			}
		}
		return s.store.GoogleUser(email, payload.Subject)
	}
	user, err := s.store.GetUserByID(flow.ReauthenticateUserID)
	if err != nil {
		return nil, err
	}
	if user == nil || user.Provider != "google" || user.ProviderSubject != payload.Subject {
		return nil, db.ErrProviderConflict
	}
	return user, nil
}

func googleSignInError(code string) string {
	switch code {
	case "access_denied":
		return "Google sign-in was cancelled. Return to Misty to try again."
	case "provider_conflict":
		return "This email already has a Misty account. Use its original sign-in method."
	case "registration_closed":
		return "This server isn't taking new accounts. Ask its operator for access."
	default:
		return "Could not verify your Google account. Return to Misty and try again."
	}
}

// googleSignInCompletePage hands the completion code to Misty on this
// computer through its misty:// link, and shows it for typing as a fallback.
func googleSignInCompletePage(w http.ResponseWriter, code string) {
	link := "misty://auth/google/complete?code=" + url.QueryEscape(code)
	display := code[:googleCompletionLength/2] + "-" + code[googleCompletionLength/2:]
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Referrer-Policy", "no-referrer")
	w.Header().Set("Content-Security-Policy", "default-src 'none'; style-src 'unsafe-inline'; frame-ancestors 'none'; base-uri 'none'; form-action 'none'")
	w.WriteHeader(http.StatusOK)
	fmt.Fprintf(w, `<!doctype html><html lang="en"><meta charset="utf-8"><meta name="viewport" content="width=device-width,initial-scale=1"><meta http-equiv="refresh" content="0;url=%[1]s"><title>Misty sign-in</title><style>body{background:#111;color:#fff;font:16px system-ui;display:grid;place-items:center;min-height:95vh;margin:0}main{max-width:28rem;margin:2rem;padding:2rem;border:1px solid #444;border-radius:12px}p{color:#ccc;line-height:1.6}a{color:#fff}code{font:600 1.4rem ui-monospace,monospace;letter-spacing:.08em;color:#fff}</style><main><h1>Misty sign-in</h1><p>Google confirmed your account. Misty finishes signing in when it opens. <a href="%[1]s">Open Misty</a></p><p>If Misty doesn't open, enter this code in Misty on this computer: <code>%[2]s</code></p><p>Never share this code. If you didn't start signing in to Misty on this computer, close this tab.</p></main></html>`, html.EscapeString(link), html.EscapeString(display))
}

func googleSignInPage(w http.ResponseWriter, status int, message string) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Referrer-Policy", "no-referrer")
	w.Header().Set("Content-Security-Policy", "default-src 'none'; style-src 'unsafe-inline'; frame-ancestors 'none'; base-uri 'none'; form-action 'none'")
	w.WriteHeader(status)
	fmt.Fprintf(w, `<!doctype html><html lang="en"><meta charset="utf-8"><meta name="viewport" content="width=device-width,initial-scale=1"><title>Misty sign-in</title><style>body{background:#111;color:#fff;font:16px system-ui;display:grid;place-items:center;min-height:95vh;margin:0}main{max-width:28rem;margin:2rem;padding:2rem;border:1px solid #444;border-radius:12px}p{color:#ccc;line-height:1.6}</style><main><h1>Misty sign-in</h1><p>%s</p></main></html>`, html.EscapeString(message))
}
