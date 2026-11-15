package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"unicode"

	"github.com/misty-org/misty-server/internal/platform/config"
	"github.com/misty-org/misty-server/internal/platform/db"
)

// ErrRegistrationClosed refuses a new account the server did not invite.
var ErrRegistrationClosed = errors.New("registration_closed")

// On a fresh server the first account registers itself and is the server's
// owner. Every later account comes from an invitation by an existing account (a
// Space invitation to that email that is still open) or from the operator account
// API, unless MISTY_OPEN_SIGNUP is on: anyone may then make an account, which starts
// with what MISTY_SIGNUP_LIMITS gives it, or nothing until the operator sets its limits (openSignupLimits).
//
// A self-hosted server can instead name who it's for with MISTY_ALLOWED_EMAILS: only
// those emails may make an account or sign in, invited or not. Open signup ignores it,
// and the owner is always let in, so a mistake in the list can't lock them out.
type accountCounter interface {
	AnyAccount(context.Context) (bool, error)
	PendingAccountInvitation(context.Context, string) (bool, error)
}

// openSignup reports whether anyone may make an account on this server.
func openSignup() bool {
	switch strings.ToLower(strings.TrimSpace(config.Getenv("MISTY_OPEN_SIGNUP"))) {
	case "1", "true", "on", "yes":
		return true
	}
	return false
}

// allowedEmails is the server's allowlist, or nil when it doesn't keep one.
func allowedEmails() map[string]struct{} {
	if openSignup() {
		return nil
	}
	listed := strings.FieldsFunc(config.Getenv("MISTY_ALLOWED_EMAILS"), func(r rune) bool {
		return r == ',' || unicode.IsSpace(r)
	})
	if len(listed) == 0 {
		return nil
	}
	allowed := make(map[string]struct{}, len(listed))
	for _, email := range listed {
		allowed[normalizedEmail(email)] = struct{}{}
	}
	return allowed
}

func normalizedEmail(email string) string {
	return strings.ToLower(strings.TrimSpace(email))
}

// signInAllowed reports whether an existing account may sign in or keep its session.
func signInAllowed(ctx context.Context, store interface {
	IsServerOwner(context.Context, string) (bool, error)
}, user *db.User) (bool, error) {
	allowed := allowedEmails()
	if allowed == nil {
		return true, nil
	}
	if _, listed := allowed[normalizedEmail(user.Email)]; listed {
		return true, nil
	}
	return store.IsServerOwner(ctx, user.ID)
}

func writeEmailNotAllowed(w http.ResponseWriter) {
	writeJSON(w, http.StatusForbidden, map[string]string{"code": "email_not_allowed", "message": "This email isn't on this server's list. Ask whoever runs it to add you."})
}

// openSignupLimits sets what an account made through open signup may use. With no
// MISTY_SIGNUP_LIMITS it holds the account to nothing, so it costs the server nothing until
// the operator sets its limits. MISTY_SIGNUP_LIMITS is a limits object like the operator
// API's, such as Misty Cloud's free plan:
// {"storage_bytes":5000000000,"devices":null,"spaces":2}. A field it leaves out stays 0
// for storage and devices, and unlimited for the rest.
func openSignupLimits(ctx context.Context, store interface {
	SetAccountLimits(context.Context, string, db.AccountLimits) error
}, userID string) error {
	limits, err := signupLimits()
	if err != nil {
		return err
	}
	return store.SetAccountLimits(ctx, userID, limits)
}

// signupLimits reads MISTY_SIGNUP_LIMITS onto the closed default.
func signupLimits() (db.AccountLimits, error) {
	var storage, devices int64
	base := db.AccountLimits{StorageBytes: &storage, Devices: &devices}
	raw := strings.TrimSpace(config.Getenv("MISTY_SIGNUP_LIMITS"))
	if raw == "" {
		return base, nil
	}
	var parsed operatorLimits
	if err := json.Unmarshal([]byte(raw), &parsed); err != nil {
		return base, fmt.Errorf("MISTY_SIGNUP_LIMITS is not a limits object: %w", err)
	}
	limits, err := parsed.apply(base)
	if err != nil {
		return base, fmt.Errorf("MISTY_SIGNUP_LIMITS: %w", err)
	}
	return limits, nil
}

// registrationAllowed reports whether email may create an account now.
func registrationAllowed(ctx context.Context, store accountCounter, email string) (bool, error) {
	exists, err := store.AnyAccount(ctx)
	if err != nil || !exists {
		return !exists, err
	}
	if openSignup() {
		return true, nil
	}
	if allowed := allowedEmails(); allowed != nil {
		_, listed := allowed[normalizedEmail(email)]
		return listed, nil
	}
	return store.PendingAccountInvitation(ctx, email)
}

func writeRegistrationClosed(w http.ResponseWriter) {
	if allowedEmails() != nil {
		writeEmailNotAllowed(w)
		return
	}
	writeJSON(w, http.StatusForbidden, map[string]string{"code": "registration_closed", "message": "This server takes new accounts by invitation. Ask someone who uses it to invite you."})
}
