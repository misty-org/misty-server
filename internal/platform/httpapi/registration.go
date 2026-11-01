package api

import (
	"context"
	"errors"
	"net/http"
)

// ErrRegistrationClosed refuses a new account the server did not invite.
var ErrRegistrationClosed = errors.New("registration_closed")

// Who may create an account has no setting. On a fresh server the first
// account registers itself and is the server's owner. Every later account
// comes from an invitation by an existing account (a Space invitation to that
// email that is still open) or from the operator account API.
type accountCounter interface {
	AnyAccount(context.Context) (bool, error)
	PendingAccountInvitation(context.Context, string) (bool, error)
}

// registrationAllowed reports whether email may create an account now.
func registrationAllowed(ctx context.Context, store accountCounter, email string) (bool, error) {
	exists, err := store.AnyAccount(ctx)
	if err != nil || !exists {
		return !exists, err
	}
	return store.PendingAccountInvitation(ctx, email)
}

// registrationState is what /instance reports: "open" until the first account
// exists, then "invitation", since only invited people may sign up.
func registrationState(ctx context.Context, store accountCounter) string {
	if exists, err := store.AnyAccount(ctx); err == nil && exists {
		return "invitation"
	}
	return "open"
}

func writeRegistrationClosed(w http.ResponseWriter) {
	writeJSON(w, http.StatusForbidden, map[string]string{"code": "registration_closed", "message": "This server takes new accounts by invitation. Ask someone who uses it to invite you."})
}
