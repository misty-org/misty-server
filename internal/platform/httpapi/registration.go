package api

import (
	"context"
	"errors"
	"net/http"
	"strings"

	envconfig "github.com/kannachi323/misty/server/internal/platform/config"
)

// ErrRegistrationClosed refuses a new account on a server that takes no more.
var ErrRegistrationClosed = errors.New("registration_closed")

// RegistrationMode is who may create an account (MISTY_REGISTRATION):
// "open" (anyone), "first" (only the first account; a self-hosted server
// belongs to its operator) or "closed". It defaults to "first" in production
// and "open" elsewhere. Existing accounts always sign in.
func RegistrationMode() string {
	switch mode := strings.ToLower(strings.TrimSpace(envconfig.Getenv("MISTY_REGISTRATION"))); mode {
	case "open", "first", "closed":
		return mode
	}
	if strings.EqualFold(strings.TrimSpace(envconfig.Getenv("MISTY_ENVIRONMENT")), "production") {
		return "first"
	}
	return "open"
}

type accountCounter interface {
	AnyAccount(context.Context) (bool, error)
}

// registrationAllowed reports whether a new account may be created now.
func registrationAllowed(ctx context.Context, store accountCounter) (bool, error) {
	switch RegistrationMode() {
	case "open":
		return true, nil
	case "closed":
		return false, nil
	}
	exists, err := store.AnyAccount(ctx)
	return !exists, err
}

// registrationState is what /instance reports: "open" while anyone may sign
// up, "closed" once this server takes no more accounts.
func registrationState(ctx context.Context, store accountCounter) string {
	if allowed, err := registrationAllowed(ctx, store); err == nil && !allowed {
		return "closed"
	}
	return "open"
}

func writeRegistrationClosed(w http.ResponseWriter) {
	writeJSON(w, http.StatusForbidden, map[string]string{"code": "registration_closed", "message": "This server isn't taking new accounts. Ask its operator for access."})
}
