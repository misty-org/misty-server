package api

import (
	"context"
	"crypto/subtle"
	"errors"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/kannachi323/misty/server/internal/platform/config"
	"github.com/kannachi323/misty/server/internal/platform/db"
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
	FinishGoogleSignInFlow(context.Context, string, string, string, string) error
	ConsumeGoogleSignInFlow(context.Context, string, string) (*db.GoogleSignInFlow, error)
	GoogleUser(string, string) (*db.User, error)
	GetUserByID(string) (*db.User, error)
	CreateGoogleReauthenticationToken(context.Context, string, string) error
}

type GoogleSignInService struct {
	store    googleSignInStore
	oauth    oauth2.Config
	startURL string
	validate func(context.Context, string, string) (*idtoken.Payload, error)
	session  func(http.ResponseWriter, *http.Request, *db.User)
	// verification marks Google accounts verified and records signup signals.
	verification *AccountVerification
}

// SetVerification lets Google sign-ins mark their (Google-verified) email.
func (s *GoogleSignInService) SetVerification(v *AccountVerification) { s.verification = v }

func NewGoogleSignInService(database *db.Database) (*GoogleSignInService, error) {
	s := &GoogleSignInService{store: database, validate: idtoken.Validate}
	s.session = func(w http.ResponseWriter, r *http.Request, u *db.User) {
		writeAuthSession(w, r, database, u, http.StatusOK)
	}
	clientID := strings.TrimSpace(config.Getenv("GOOGLE_CLIENT_ID"))
	secret := strings.TrimSpace(config.Getenv("GOOGLE_CLIENT_SECRET"))
	if clientID == "" || secret == "" {
		return s, nil
	}
	callback := strings.TrimSpace(config.Getenv("GOOGLE_SIGN_IN_REDIRECT_URL"))
	if callback == "" {
		base := strings.TrimRight(strings.TrimSpace(config.Getenv("MISTY_PUBLIC_API_URL")), "/")
		if base == "" {
			return s, nil
		}
		callback = base + "/auth/google/callback"
	}
	u, err := url.Parse(callback)
	if err != nil || ValidateResetURL(callback) != nil || u.User != nil || u.RawQuery != "" || u.ForceQuery || u.Fragment != "" || !strings.HasSuffix(u.Path, "/auth/google/callback") {
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
					user, accountErr := s.googleAccount(flow, payload, email)
					if accountErr == nil && user != nil {
						userID = user.ID
						errorCode = ""
					} else if errors.Is(accountErr, db.ErrProviderConflict) {
						errorCode = "provider_conflict"
					} else if errors.Is(accountErr, ErrRegistrationClosed) {
						errorCode = "registration_closed"
					}
				}
			}
		}
		completion, completionHash := "", ""
		if errorCode == "" {
			if completion, err = newGoogleCompletionCode(); err != nil {
				userID, errorCode = "", "google_sign_in_failed"
			} else {
				completionHash = security.HashToken(completion)
			}
		}
		// Persist a terminal result even if the browser disconnects while exchanging.
		finishCtx, finishCancel := context.WithTimeout(context.WithoutCancel(r.Context()), 5*time.Second)
		defer finishCancel()
		if err = s.store.FinishGoogleSignInFlow(finishCtx, flow.StateHash, userID, errorCode, completionHash); err != nil {
			googleSignInPage(w, 500, "Could not finish sign-in. Return to Misty and try again.")
			return
		}
		if errorCode != "" {
			googleSignInPage(w, 400, googleSignInError(errorCode))
			return
		}
		googleSignInCompletePage(w, completion)
	}
}
