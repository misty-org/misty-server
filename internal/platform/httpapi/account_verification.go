package api

import (
	"context"
	"errors"
	"log"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/kannachi323/misty/server/internal/platform/config"
	"github.com/kannachi323/misty/server/internal/platform/db"
	"github.com/kannachi323/misty/server/internal/platform/email"
	"github.com/kannachi323/misty/server/internal/platform/security"
)

const emailVerificationTTL = 24 * time.Hour

// AccountVerification confirms new accounts' emails.
type AccountVerification struct {
	database *db.Database
	sender   email.EmailVerificationSender
	now      func() time.Time
}

func NewAccountVerification(database *db.Database, sender email.EmailVerificationSender) *AccountVerification {
	return &AccountVerification{database: database, sender: sender, now: time.Now}
}

// AfterSignup sends a password account its verification email. Identity
// providers that verify email mark it verified.
func (v *AccountVerification) AfterSignup(r *http.Request, user *db.User, providerVerified bool) {
	if v == nil || user == nil {
		return
	}
	ctx := context.WithoutCancel(r.Context())
	if providerVerified {
		if err := v.database.MarkEmailVerified(ctx, user.ID); err != nil {
			log.Printf("mark email verified for %s: %v", user.ID, err)
		}
		return
	}
	if err := v.send(ctx, r, user.ID, user.Email); err != nil {
		log.Printf("email verification send for %s: %v", user.ID, err)
	}
}

func (v *AccountVerification) send(ctx context.Context, r *http.Request, userID, address string) error {
	token, err := security.GenerateSecureToken()
	if err != nil {
		return err
	}
	if err = v.database.UpsertEmailVerificationToken(ctx, userID, security.HashToken(token), v.now().Add(emailVerificationTTL)); err != nil {
		return err
	}
	base := strings.TrimRight(strings.TrimSpace(config.Getenv("MISTY_PUBLIC_API_URL")), "/")
	if base == "" {
		scheme := "http"
		if IsSecureRequest(r) {
			scheme = "https"
		}
		base = scheme + "://" + r.Host
	}
	return v.sender.SendEmailVerification(ctx, address, base+"/auth/verify/start?token="+url.QueryEscape(token))
}

// Verify consumes the emailed link.
func (v *AccountVerification) Verify() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		token := strings.TrimSpace(r.URL.Query().Get("token"))
		if token == "" {
			googleSignInPage(w, http.StatusBadRequest, "This confirmation link is incomplete. Open the newest email from Misty, or send a new link from Settings.")
			return
		}
		_, err := v.database.VerifyEmailWithToken(r.Context(), security.HashToken(token), v.now())
		if errors.Is(err, db.ErrEmailVerificationInvalid) {
			googleSignInPage(w, http.StatusGone, "This confirmation link has expired or was already used. Send a new one from Settings in Misty.")
			return
		}
		if err != nil {
			googleSignInPage(w, http.StatusServiceUnavailable, "We could not confirm your email right now. Try the link again in a minute.")
			return
		}
		googleSignInPage(w, http.StatusOK, "Your email is confirmed. Return to Misty.")
	}
}

// Resend sends a fresh link to the signed-in account, at most once a minute.
func (v *AccountVerification) Resend() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		userID, err := sessionUserID(r, v.database)
		if err != nil || userID == "" {
			http.Error(w, "not authenticated", http.StatusUnauthorized)
			return
		}
		verified, err := v.database.EmailVerifiedAt(r.Context(), userID)
		if err != nil {
			http.Error(w, "internal error", http.StatusInternalServerError)
			return
		}
		if verified != nil {
			writeJSON(w, http.StatusOK, map[string]any{"verified": true})
			return
		}
		if sent, err := v.database.EmailVerificationSentAt(r.Context(), userID); err == nil && sent != nil && v.now().Sub(*sent) < time.Minute {
			writeJSON(w, http.StatusTooManyRequests, map[string]string{"code": "verification_recently_sent", "message": "A link was just sent. Check your inbox, then try again in a minute."})
			return
		}
		user, err := v.database.GetUserByID(userID)
		if err != nil || user == nil {
			http.Error(w, "account unavailable", http.StatusServiceUnavailable)
			return
		}
		if err = v.send(r.Context(), r, userID, user.Email); err != nil {
			writeJSON(w, http.StatusServiceUnavailable, map[string]string{"code": "verification_unsent", "message": "The email could not be sent. Try again shortly."})
			return
		}
		writeJSON(w, http.StatusAccepted, map[string]any{"verified": false, "sent": true})
	}
}
