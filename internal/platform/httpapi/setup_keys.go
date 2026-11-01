package api

import (
	"crypto/rand"
	"encoding/base32"
	"errors"
	"log"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/kannachi323/misty/server/internal/platform/db"
	"github.com/kannachi323/misty/server/internal/platform/security"
)

// A setup key is "misty_setup_" and 32 base32 characters (160 random bits).
// The operator account API and the console issue one as a sign-in link; it
// signs that account in once, within db.SetupKeyTTL. The server sends no
// email, so a setup key is also how a forgotten password is reset.
const setupKeyPrefix = "misty_setup_"

var setupKeyEncoding = base32.NewEncoding("abcdefghijklmnopqrstuvwxyz234567").WithPadding(base32.NoPadding)

// GenerateSetupKey returns a new key; only its hash (security.HashToken) is stored.
func GenerateSetupKey() (string, error) {
	var random [20]byte
	if _, err := rand.Read(random[:]); err != nil {
		return "", err
	}
	return setupKeyPrefix + setupKeyEncoding.EncodeToString(random[:]), nil
}

// NormalizeSetupKey trims a pasted key and accepts either case. ok is false
// when it cannot be a setup key.
func NormalizeSetupKey(raw string) (string, bool) {
	key := strings.ToLower(strings.TrimSpace(raw))
	body, found := strings.CutPrefix(key, setupKeyPrefix)
	if !found || len(body) != 32 {
		return "", false
	}
	if _, err := setupKeyEncoding.DecodeString(body); err != nil {
		return "", false
	}
	return key, true
}

// RedeemSetupKey signs the key's account in exactly as POST /login does (the
// same response body, cookies and session) and marks the key used. Failures
// count toward the same lockout as sign-in, per client address. A password
// account's answer also carries password_reset_token, which lets PUT
// /me/password set a new password without the old one within
// db.PasswordResetTTL.
func RedeemSetupKey(database *db.Database) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			SetupKey string `json:"setup_key"`
		}
		if decodeJSON(w, r, &body) != nil {
			return
		}
		failureKey := "setup-key:" + ClientIPFromRequest(r)
		if blocked, retryAfter := loginFailures.Blocked(failureKey, time.Now()); blocked {
			w.Header().Set("Retry-After", strconv.Itoa(retryAfterSeconds(retryAfter)))
			http.Error(w, "too many sign-in attempts; try again later", http.StatusTooManyRequests)
			return
		}
		key, ok := NormalizeSetupKey(body.SetupKey)
		if !ok {
			loginFailures.Fail(failureKey, time.Now())
			writeSetupKeyInvalid(w)
			return
		}
		user, err := database.RedeemSetupKey(r.Context(), security.HashToken(key))
		switch {
		case errors.Is(err, db.ErrSetupKeyInvalid):
			loginFailures.Fail(failureKey, time.Now())
			writeSetupKeyInvalid(w)
			return
		case errors.Is(err, db.ErrAccountSuspended):
			writeAccountSuspended(w)
			return
		case err != nil:
			http.Error(w, "internal error", http.StatusInternalServerError)
			return
		}
		loginFailures.Succeed(failureKey)
		var extra map[string]any
		if user.Provider == "misty" {
			if reset, err := security.GenerateSecureToken(); err == nil {
				if err = database.GrantPasswordReset(r.Context(), user.ID, security.HashToken(reset), time.Now().Add(db.PasswordResetTTL)); err == nil {
					extra = map[string]any{"password_reset_token": reset}
				} else {
					log.Printf("setup key password reset grant for %s: %v", user.ID, err)
				}
			}
		}
		writeAuthSessionWith(w, r, database, user, http.StatusOK, extra)
	}
}

func writeSetupKeyInvalid(w http.ResponseWriter) {
	writeJSON(w, http.StatusUnauthorized, map[string]string{"code": "setup_key_invalid", "message": "This setup key is not valid. It may be used or expired; ask for a new one."})
}
