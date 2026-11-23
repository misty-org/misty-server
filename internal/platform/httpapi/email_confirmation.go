package api

import (
	"errors"
	"log"
	"net/http"
	"net/url"
	"strings"

	"golang.org/x/crypto/bcrypt"

	"github.com/misty-org/misty-server/internal/platform/db"
	"github.com/misty-org/misty-server/internal/platform/email"
	"github.com/misty-org/misty-server/internal/platform/security"
)

func confirmsEmail() bool { return email.Configured() }

func confirmationLink(serverOrigin, token string) string {
	return websiteURL() + "/confirm-email#server=" + url.QueryEscape(serverHost(serverOrigin)) + "&token=" + url.QueryEscape(token)
}

func newConfirmation(r *http.Request) (hash, link string, err error) {
	token, err := security.GenerateSecureToken()
	if err != nil {
		return "", "", err
	}
	return security.HashToken(token), confirmationLink(publicServerOrigin(r), token), nil
}

func holdForConfirmation(w http.ResponseWriter, r *http.Request, database *db.Database, account db.PendingAccount) {
	hash, link, err := newConfirmation(r)
	if err == nil {
		err = database.QueuePendingAccount(r.Context(), account, hash, link)
	}
	switch {
	case errors.Is(err, db.ErrInvalidUsername):
		http.Error(w, err.Error(), http.StatusBadRequest)
	case errors.Is(err, db.ErrUsernameTaken):
		http.Error(w, err.Error(), http.StatusConflict)
	case err != nil:
		log.Printf("sign-up not held for confirmation: %v", err)
		http.Error(w, "failed to create user", http.StatusInternalServerError)
	default:
		writeJSON(w, http.StatusAccepted, map[string]string{"status": "confirm_email"})
	}
}

func pendingSignIn(w http.ResponseWriter, r *http.Request, database *db.Database, address, password string, waiting bool) (handled bool) {
	hash, err := database.PendingAccountPassword(r.Context(), address)
	if err != nil {
		log.Printf("pending sign-up not read: %v", err)
	}
	if hash == "" {
		_ = bcrypt.CompareHashAndPassword(unknownAccountHash(), []byte(password))
		return false
	}
	if bcrypt.CompareHashAndPassword([]byte(hash), []byte(password)) != nil {
		return false
	}
	if waiting {
		writeJSON(w, http.StatusAccepted, map[string]string{"status": "confirm_email"})
		return true
	}
	tokenHash, link, err := newConfirmation(r)
	if err == nil {
		err = database.ResendPendingAccount(r.Context(), address, tokenHash, link)
	}
	if err != nil {
		log.Printf("confirmation link not sent again: %v", err)
	}
	writeJSON(w, http.StatusForbidden, map[string]string{"code": "email_not_confirmed", "message": "Confirm your email first. The link is in your inbox."})
	return true
}

func confirmPending(r *http.Request, database *db.Database, token string) (*db.User, string) {
	user, err := database.ConfirmPendingAccount(r.Context(), security.HashToken(strings.TrimSpace(token)))
	if err == nil && openSignup() {
		err = openSignupLimits(r.Context(), database, user.ID)
	}
	switch {
	case err == nil:
		return user, "confirmed"
	case errors.Is(err, db.ErrPendingAccountInvalid):
		return nil, "expired"
	case errors.Is(err, db.ErrEmailTaken):
		return nil, "exists"
	default:
		log.Printf("email not confirmed: %v", err)
		return nil, "failed"
	}
}

func ConfirmEmail(database *db.Database) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		r.Body = http.MaxBytesReader(w, r.Body, 4096)
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("Referrer-Policy", "no-referrer")
		_, result := confirmPending(r, database, r.PostFormValue("token"))
		http.Redirect(w, r, websiteURL()+"/confirm-email?result="+result, http.StatusSeeOther)
	}
}

func ConfirmEmailSignIn(database *db.Database) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Token string `json:"token"`
		}
		if decodeJSON(w, r, &body) != nil {
			return
		}
		user, result := confirmPending(r, database, body.Token)
		if user == nil {
			status := http.StatusInternalServerError
			switch result {
			case "expired":
				status = http.StatusBadRequest
			case "exists":
				status = http.StatusConflict
			}
			writeJSON(w, status, map[string]string{"code": result})
			return
		}
		writeAuthSession(w, r, database, user, http.StatusOK)
	}
}
