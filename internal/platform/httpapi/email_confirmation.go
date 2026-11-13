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

// A server that sends email makes a new password account only once its email is
// confirmed. Until then the sign-up waits in pending_accounts: no account, no session.
// Otherwise someone could hold another person's address, and that person could never sign
// up with it. The server's first account is not held, and a server without email can't ask.
func confirmsEmail() bool { return email.Configured() }

// confirmationLink is the website's static page for a confirmation token, which
// reads the server and token from the fragment and posts them to ConfirmEmail.
func confirmationLink(serverOrigin, token string) string {
	return websiteURL() + "/confirm-email#server=" + url.QueryEscape(serverHost(serverOrigin)) + "&token=" + url.QueryEscape(token)
}

// newConfirmation makes a token, and the hash and link that go with it.
func newConfirmation(r *http.Request) (hash, link string, err error) {
	token, err := security.GenerateSecureToken()
	if err != nil {
		return "", "", err
	}
	return security.HashToken(token), confirmationLink(publicServerOrigin(r), token), nil
}

// holdForConfirmation answers a sign-up on a server that confirms email. The answer
// is the same whether or not a new email went out.
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

// pendingSignIn handles a sign-in for an email with no account. It spends the same
// bcrypt work whether or not a sign-up is waiting. The right password for one gets a new
// link and the reason, and handled is true; anything else goes on to fail like any
// unknown email. An app asking with waiting is told to keep waiting and sends nothing,
// since a new link would stop the one in the person's inbox.
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

// confirmPending makes the account a confirmation token stands for. The result names what
// happened, in the words the website's page knows; only "confirmed" comes with the account.
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

// ConfirmEmail takes the form on the website's confirmation page and sends the person
// back there with the result. It is a form because that page is on another site, for
// every server. The token is all it needs: it opens no session, and the app the person
// signed up in signs itself in. The page posts it when opened, so a mail scanner that only
// fetches links confirms nothing, though one that runs the page in a browser could.
func ConfirmEmail(database *db.Database) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		r.Body = http.MaxBytesReader(w, r.Body, 4096)
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("Referrer-Policy", "no-referrer")
		_, result := confirmPending(r, database, r.PostFormValue("token"))
		http.Redirect(w, r, websiteURL()+"/confirm-email?result="+result, http.StatusSeeOther)
	}
}

// ConfirmEmailSignIn is ConfirmEmail for the website on its own server, where it has
// sessions: the same token also signs the new account in, as POST /login does. A token
// whose email gained an account meanwhile signs nobody in. The cookies have to land where
// the website's requests go, so this is a request of the page's, not its form.
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
