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

var ErrRegistrationClosed = errors.New("registration_closed")

type accountCounter interface {
	AnyAccount(context.Context) (bool, error)
	PendingAccountInvitation(context.Context, string) (bool, error)
}

func openSignup() bool {
	switch strings.ToLower(strings.TrimSpace(config.Getenv("MISTY_OPEN_SIGNUP"))) {
	case "1", "true", "on", "yes":
		return true
	}
	return false
}

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

func openSignupLimits(ctx context.Context, store interface {
	SetAccountLimits(context.Context, string, db.AccountLimits) error
}, userID string) error {
	limits, err := signupLimits()
	if err != nil {
		return err
	}
	return store.SetAccountLimits(ctx, userID, limits)
}

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
