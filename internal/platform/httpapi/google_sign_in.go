package api

import (
	"context"
	"crypto/subtle"
	"errors"
	"fmt"
	"html"
	"net/http"
	"net/url"
	"strings"
	"time"

	envconfig "github.com/kannachi323/misty/server/internal/platform/config"
	db "github.com/kannachi323/misty/server/internal/platform/postgres"
	"github.com/kannachi323/misty/server/internal/platform/security"
	"golang.org/x/oauth2"
	"golang.org/x/oauth2/google"
	"google.golang.org/api/idtoken"
)

const googleSignInTTL = 10 * time.Minute
const googleSignInCookie = "misty_google_sign_in"

type googleSignInStore interface {
	CreateGoogleSignInFlow(context.Context, db.GoogleSignInFlow) error
	AdvanceGoogleSignInFlow(context.Context, string, string, string) (db.GoogleSignInFlow, error)
	FinishGoogleSignInFlow(context.Context, string, string, string) error
	ConsumeGoogleSignInFlow(context.Context, string) (*db.GoogleSignInFlow, error)
	GoogleUser(string, string, string) (*db.User, error)
	GetUserByID(string) (*db.User, error)
	CreateGoogleReauthenticationToken(context.Context, string, string) error
}

type GoogleSignInService struct {
	store    googleSignInStore
	oauth    oauth2.Config
	startURL string
	validate func(context.Context, string, string) (*idtoken.Payload, error)
	session  func(http.ResponseWriter, *http.Request, *db.User)
}

func NewGoogleSignInService(database *db.Database) (*GoogleSignInService, error) {
	s := &GoogleSignInService{store: database, validate: idtoken.Validate}
	s.session = func(w http.ResponseWriter, r *http.Request, u *db.User) {
		writeAuthSession(w, r, database, u, http.StatusOK)
	}
	clientID := strings.TrimSpace(envconfig.Getenv("GOOGLE_CLIENT_ID"))
	secret := strings.TrimSpace(envconfig.Getenv("GOOGLE_CLIENT_SECRET"))
	if clientID == "" || secret == "" {
		return s, nil
	}
	callback := strings.TrimSpace(envconfig.Getenv("GOOGLE_SIGN_IN_REDIRECT_URL"))
	if callback == "" {
		base := strings.TrimRight(strings.TrimSpace(envconfig.Getenv("MISTY_PUBLIC_API_URL")), "/")
		if base == "" {
			return s, nil
		}
		callback = base + "/auth/google/callback"
	}
	u, err := url.Parse(callback)
	if err != nil || TestingValidateResetURL(callback) != nil || u.User != nil || u.RawQuery != "" || u.ForceQuery || u.Fragment != "" || !strings.HasSuffix(u.Path, "/auth/google/callback") {
		return nil, errors.New("GOOGLE_SIGN_IN_REDIRECT_URL must be an HTTPS API URL ending in /auth/google/callback (HTTP allowed on localhost)")
	}
	s.oauth = oauth2.Config{ClientID: clientID, ClientSecret: secret, RedirectURL: callback, Endpoint: google.Endpoint, Scopes: []string{"openid", "email", "profile"}}
	s.startURL = strings.TrimSuffix(callback, "callback") + "start"
	return s, nil
}

func (s *GoogleSignInService) Available() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		writeJSON(w, http.StatusOK, map[string]bool{"enabled": s.oauth.ClientID != ""})
	}
}

func (s *GoogleSignInService) enabled(w http.ResponseWriter) bool {
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Referrer-Policy", "no-referrer")
	if s.oauth.ClientID == "" {
		http.Error(w, "Google sign-in is not configured", http.StatusServiceUnavailable)
		return false
	}
	return true
}

func (s *GoogleSignInService) Begin() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !s.enabled(w) {
			return
		}
		var body struct {
			Reauthenticate bool `json:"reauthenticate"`
		}
		if decodeJSON(w, r, &body) != nil {
			return
		}
		reauthenticateID := ""
		if body.Reauthenticate {
			id, err := sessionUserID(r, nil)
			if err != nil || id == "" {
				http.Error(w, "sign in before reauthenticating", http.StatusUnauthorized)
				return
			}
			user, err := s.store.GetUserByID(id)
			if err != nil || user == nil || user.Provider != "google" {
				http.Error(w, "Google reauthentication is unavailable", http.StatusForbidden)
				return
			}
			reauthenticateID = id
		}
		state, err := security.GenerateSecureToken()
		if err != nil {
			http.Error(w, "internal error", 500)
			return
		}
		poll, err := security.GenerateSecureToken()
		if err != nil {
			http.Error(w, "internal error", 500)
			return
		}
		nonce, err := security.GenerateSecureToken()
		if err != nil {
			http.Error(w, "internal error", 500)
			return
		}
		flow := db.GoogleSignInFlow{StateHash: security.HashToken(state), PollHash: security.HashToken(poll), Nonce: nonce, Verifier: oauth2.GenerateVerifier(), ExpiresAt: time.Now().Add(googleSignInTTL)}
		flow.ReauthenticateUserID = reauthenticateID
		if err = s.store.CreateGoogleSignInFlow(r.Context(), flow); err != nil {
			http.Error(w, "internal error", 500)
			return
		}
		writeJSON(w, http.StatusCreated, map[string]any{"url": s.startURL + "?state=" + url.QueryEscape(state), "flow_token": poll, "expires_in": int(googleSignInTTL.Seconds())})
	}
}

func (s *GoogleSignInService) Start() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !s.enabled(w) {
			return
		}
		state := r.URL.Query().Get("state")
		if state == "" || len(r.URL.Query()["state"]) != 1 {
			googleSignInPage(w, 400, "Invalid Google sign-in. Return to Misty and try again.")
			return
		}
		flow, err := s.store.AdvanceGoogleSignInFlow(r.Context(), security.HashToken(state), "pending", "started")
		if err != nil {
			googleSignInPage(w, 400, "This sign-in link has expired. Return to Misty and try again.")
			return
		}
		http.SetCookie(w, &http.Cookie{Name: googleSignInCookie, Value: state, Path: "/", HttpOnly: true, Secure: strings.HasPrefix(s.oauth.RedirectURL, "https:"), SameSite: http.SameSiteLaxMode, MaxAge: int(googleSignInTTL.Seconds())})
		options := []oauth2.AuthCodeOption{oauth2.S256ChallengeOption(flow.Verifier), oauth2.SetAuthURLParam("nonce", flow.Nonce), oauth2.SetAuthURLParam("prompt", "select_account")}
		http.Redirect(w, r, s.oauth.AuthCodeURL(state, options...), http.StatusSeeOther)
	}
}

func (s *GoogleSignInService) Callback() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !s.enabled(w) {
			return
		}
		state := r.URL.Query().Get("state")
		cookie, err := r.Cookie(googleSignInCookie)
		if err != nil || state == "" || len(r.URL.Query()["state"]) != 1 || subtle.ConstantTimeCompare([]byte(state), []byte(cookie.Value)) != 1 {
			googleSignInPage(w, 400, "Google sign-in could not be verified. Return to Misty and try again.")
			return
		}
		http.SetCookie(w, &http.Cookie{Name: googleSignInCookie, Value: "", Path: "/", HttpOnly: true, Secure: strings.HasPrefix(s.oauth.RedirectURL, "https:"), SameSite: http.SameSiteLaxMode, MaxAge: -1})
		flow, err := s.store.AdvanceGoogleSignInFlow(r.Context(), security.HashToken(state), "started", "exchanging")
		if err != nil {
			googleSignInPage(w, 400, "This Google sign-in has expired or was already used.")
			return
		}
		userID, errorCode := "", "google_sign_in_failed"
		ctx, cancel := context.WithTimeout(r.Context(), 20*time.Second)
		defer cancel()
		if r.URL.Query().Get("error") == "access_denied" {
			errorCode = "access_denied"
		} else if r.URL.Query().Get("error") == "" && len(r.URL.Query()["code"]) == 1 && r.URL.Query().Get("code") != "" {
			token, exchangeErr := s.oauth.Exchange(ctx, r.URL.Query().Get("code"), oauth2.VerifierOption(flow.Verifier))
			if exchangeErr == nil {
				raw, _ := token.Extra("id_token").(string)
				payload, validationErr := s.validate(ctx, raw, s.oauth.ClientID)
				if validationErr == nil && validGoogleIdentity(payload, flow.Nonce, s.oauth.ClientID) {
					email, _ := payload.Claims["email"].(string)
					name, _ := payload.Claims["name"].(string)
					user, accountErr := s.googleAccount(flow, payload, name, email)
					if accountErr == nil && user != nil {
						userID = user.ID
						errorCode = ""
					} else if errors.Is(accountErr, db.ErrProviderConflict) {
						errorCode = "provider_conflict"
					}
				}
			}
		}
		// Persist a terminal result even if the browser disconnects while exchanging.
		finishCtx, finishCancel := context.WithTimeout(context.WithoutCancel(r.Context()), 5*time.Second)
		defer finishCancel()
		if err = s.store.FinishGoogleSignInFlow(finishCtx, flow.StateHash, userID, errorCode); err != nil {
			googleSignInPage(w, 500, "Could not finish sign-in. Return to Misty and try again.")
			return
		}
		if errorCode != "" {
			googleSignInPage(w, 400, googleSignInError(errorCode))
			return
		}
		googleSignInPage(w, 200, "You're signed in. Return to Misty to continue. You can close this tab.")
	}
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
			Token string `json:"flow_token"`
		}
		if decodeJSON(w, r, &body) != nil {
			return
		}
		if body.Token == "" || len(body.Token) > 256 {
			http.Error(w, "invalid sign-in token", 400)
			return
		}
		flow, err := s.store.ConsumeGoogleSignInFlow(r.Context(), security.HashToken(body.Token))
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
		s.session(w, r, user)
	}
}

func (s *GoogleSignInService) googleAccount(flow db.GoogleSignInFlow, payload *idtoken.Payload, name, email string) (*db.User, error) {
	if flow.ReauthenticateUserID == "" {
		return s.store.GoogleUser(name, email, payload.Subject)
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
	default:
		return "Could not verify your Google account. Return to Misty and try again."
	}
}

func googleSignInPage(w http.ResponseWriter, status int, message string) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Referrer-Policy", "no-referrer")
	w.Header().Set("Content-Security-Policy", "default-src 'none'; style-src 'unsafe-inline'; frame-ancestors 'none'; base-uri 'none'; form-action 'none'")
	w.WriteHeader(status)
	fmt.Fprintf(w, `<!doctype html><html lang="en"><meta charset="utf-8"><meta name="viewport" content="width=device-width,initial-scale=1"><title>Misty sign-in</title><style>body{background:#111;color:#fff;font:16px system-ui;display:grid;place-items:center;min-height:95vh;margin:0}main{max-width:28rem;margin:2rem;padding:2rem;border:1px solid #444;border-radius:12px}p{color:#ccc;line-height:1.6}</style><main><h1>Misty sign-in</h1><p>%s</p></main></html>`, html.EscapeString(message))
}
