package api

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"log"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/kannachi323/misty/server/internal/billingadapter"
	envconfig "github.com/kannachi323/misty/server/internal/platform/config"
	"github.com/kannachi323/misty/server/internal/platform/email"
	db "github.com/kannachi323/misty/server/internal/platform/postgres"
	"github.com/kannachi323/misty/server/internal/platform/security"
)

const emailVerificationTTL = 24 * time.Hour

// AccountVerification confirms new accounts' emails before they can use AI and
// carries referral claims to billing once an account is verified.
type AccountVerification struct {
	database *db.Database
	sender   email.EmailVerificationSender
	secret   []byte
	now      func() time.Time
}

func NewAccountVerification(database *db.Database, sender email.EmailVerificationSender) *AccountVerification {
	secret := []byte(envconfig.Getenv("MISTY_SIGNUP_SIGNAL_SECRET"))
	if len(secret) < 16 {
		// Signals compare within one deployment; the billing secret is stable there.
		secret = []byte("misty-signup-signals:" + envconfig.Getenv("MISTY_BILLING_SECRET"))
	}
	return &AccountVerification{database: database, sender: sender, secret: secret, now: time.Now}
}

// Gate refuses AI admission for an account whose email is unverified.
func (v *AccountVerification) Gate(ctx context.Context, req billingadapter.Request) error {
	// Only AI needs a confirmed email; sync and storage never wait on it.
	if !strings.HasPrefix(req.Operation, "agent") && !strings.HasPrefix(req.Operation, "library.") && !strings.HasPrefix(req.Operation, "web.") {
		return nil
	}
	verified, err := v.database.EmailVerifiedAt(ctx, req.AccountID)
	if err != nil {
		return err
	}
	if verified == nil {
		return &billingadapter.Denial{Limit: "email"}
	}
	return nil
}

// signal hashes a value so raw addresses and install IDs are never stored.
func (v *AccountVerification) signal(value string) string {
	if value == "" {
		return ""
	}
	mac := hmac.New(sha256.New, v.secret)
	mac.Write([]byte(value))
	return hex.EncodeToString(mac.Sum(nil))
}

// signupSignals are the hashed network (IPv4 /24, IPv6 /48) and install.
func (v *AccountVerification) signupSignals(r *http.Request) (string, string) {
	network := ""
	if ip := net.ParseIP(TestingClientIPFromRequest(r)); ip != nil {
		if v4 := ip.To4(); v4 != nil {
			network = v4.Mask(net.CIDRMask(24, 32)).String()
		} else {
			network = ip.Mask(net.CIDRMask(48, 128)).String()
		}
	}
	return v.signal(network), v.signal(strings.TrimSpace(r.Header.Get("X-Misty-Install-Id")))
}

// AfterSignup records signup signals and, for a password account, sends the
// verification email. Identity providers that verify email mark it verified.
func (v *AccountVerification) AfterSignup(r *http.Request, user *db.User, providerVerified bool) {
	if v == nil || user == nil {
		return
	}
	ctx := context.WithoutCancel(r.Context())
	network, device := v.signupSignals(r)
	if err := v.database.RecordSignupSignals(ctx, user.ID, network, device); err != nil {
		log.Printf("signup signals for %s: %v", user.ID, err)
	}
	if providerVerified {
		if err := v.database.MarkEmailVerified(ctx, user.ID); err != nil {
			log.Printf("mark email verified for %s: %v", user.ID, err)
		}
		v.sendReferral(ctx, user.ID)
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
	base := strings.TrimRight(strings.TrimSpace(envconfig.Getenv("MISTY_PUBLIC_API_URL")), "/")
	if base == "" {
		scheme := "http"
		if TestingIsSecureRequest(r) {
			scheme = "https"
		}
		base = scheme + "://" + r.Host
	}
	return v.sender.SendEmailVerification(ctx, address, base+"/auth/verify/start?token="+url.QueryEscape(token))
}

// sendReferral delivers a verified account's referral claim to billing. A
// failure leaves the claim unsent; the next referral read retries it.
func (v *AccountVerification) sendReferral(ctx context.Context, userID string) {
	claim, err := v.database.PendingReferralClaim(ctx, userID)
	if err != nil || claim == nil || claim.Sent {
		return
	}
	verified, err := v.database.EmailVerifiedAt(ctx, userID)
	if err != nil || verified == nil {
		return
	}
	billing := v.database.BillingService()
	if !billing.Adapter.Enabled() {
		return
	}
	request := billingadapter.Request{Version: 1, AccountID: userID, Operation: "customer.referral", OperationID: "referral", Key: "referral",
		Referral: &billingadapter.Referral{ReferrerID: claim.ReferrerID, EmailVerified: true, SameDevice: claim.SameDevice, SameNetwork: claim.SameNetwork}}
	call, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	// A refusal is final too: billing recorded why the referral will not count.
	if _, err = billing.Adapter.Do(call, "referral", request); err == nil || errors.Is(err, billingadapter.ErrDenied) {
		_ = v.database.MarkReferralClaimSent(ctx, userID)
	}
}

// Verify consumes the emailed link.
func (v *AccountVerification) Verify() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		token := strings.TrimSpace(r.URL.Query().Get("token"))
		if token == "" {
			googleSignInPage(w, http.StatusBadRequest, "This confirmation link is incomplete. Open the newest email from Misty, or send a new link from Settings.")
			return
		}
		userID, err := v.database.VerifyEmailWithToken(r.Context(), security.HashToken(token), v.now())
		if errors.Is(err, db.ErrEmailVerificationInvalid) {
			googleSignInPage(w, http.StatusGone, "This confirmation link has expired or was already used. Send a new one from Settings in Misty.")
			return
		}
		if err != nil {
			googleSignInPage(w, http.StatusServiceUnavailable, "We could not confirm your email right now. Try the link again in a minute.")
			return
		}
		v.sendReferral(context.WithoutCancel(r.Context()), userID)
		googleSignInPage(w, http.StatusOK, "Your email is confirmed. Return to Misty; AI is ready to use.")
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

// Referral returns the account's link and what referrals have earned it.
func (v *AccountVerification) Referral() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		userID, err := sessionUserID(r, v.database)
		if err != nil || userID == "" {
			http.Error(w, "not authenticated", http.StatusUnauthorized)
			return
		}
		v.sendReferral(r.Context(), userID)
		code, err := v.database.ReferralCode(r.Context(), userID)
		if err != nil {
			http.Error(w, "referral unavailable", http.StatusServiceUnavailable)
			return
		}
		site := strings.TrimRight(strings.TrimSpace(envconfig.Getenv("MISTY_WEBSITE_URL")), "/")
		if site == "" {
			site = "https://mistysys.com"
		}
		response := map[string]any{"code": code, "link": site + "/r/" + code}
		if summary, err := accountBillingSummary(r.Context(), v.database, userID); err == nil {
			response["referrals"] = summary["referrals"]
		}
		if claim, err := v.database.PendingReferralClaim(r.Context(), userID); err == nil {
			response["joined_through_referral"] = claim != nil
		}
		writeJSON(w, http.StatusOK, response)
	}
}

// ClaimReferral attributes a new account to the link it joined through.
func (v *AccountVerification) ClaimReferral() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		userID, err := sessionUserID(r, v.database)
		if err != nil || userID == "" {
			http.Error(w, "not authenticated", http.StatusUnauthorized)
			return
		}
		var body struct {
			Code string `json:"code"`
		}
		if decodeJSON(w, r, &body) != nil {
			return
		}
		code := strings.ToLower(strings.TrimSpace(body.Code))
		network, device := v.signupSignals(r)
		_, err = v.database.ClaimReferral(r.Context(), userID, code, network, device, v.now())
		switch {
		case errors.Is(err, db.ErrReferralCodeUnknown):
			writeJSON(w, http.StatusNotFound, map[string]string{"code": "referral_code_unknown", "message": "That invite link is not valid."})
			return
		case errors.Is(err, db.ErrReferralNotClaimable):
			writeJSON(w, http.StatusConflict, map[string]string{"code": "referral_not_claimable", "message": "Invite links apply only to new accounts, once."})
			return
		case err != nil:
			http.Error(w, "internal error", http.StatusInternalServerError)
			return
		}
		v.sendReferral(context.WithoutCancel(r.Context()), userID)
		writeJSON(w, http.StatusOK, map[string]any{"claimed": true})
	}
}
