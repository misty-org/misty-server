package api

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"log"
	"net/http"
	"strconv"
	"time"

	"github.com/misty-org/misty-server/internal/accounts"
	"github.com/misty-org/misty-server/internal/platform/db"
)

// Login is limited per address by the API limiter. On top of that, failed passwords are
// counted twice: for the email from one address, which stops one caller guessing, and for
// the email from every address together, which bounds guessing spread across many. The
// first is low and the second higher, so a stranger who knows someone's email can't lock
// them out from a single address. A success clears both. Counts live in the database
// (db.LoginFailuresBlocked), so restarts and other instances see the same ones.
const (
	loginFailureWindow       = 15 * time.Minute
	loginFailureLimit        = 10
	loginFailureAccountLimit = 30
)

type loginFailureStore interface {
	LoginFailuresBlocked(ctx context.Context, caps []db.LoginFailureCap, window time.Duration) (bool, time.Duration, error)
	RecordLoginFailure(ctx context.Context, keys []string, window time.Duration) error
	ClearLoginFailures(ctx context.Context, keys []string) error
}

// failureKey is what a count is stored under: a hash, so the table never holds an email
// or an address.
func failureKey(kind, value string) string {
	sum := sha256.Sum256([]byte(kind + "\n" + value))
	return hex.EncodeToString(sum[:])
}

// passwordFailureCaps are the counts a wrong password for email adds to.
func passwordFailureCaps(r *http.Request, email string) []db.LoginFailureCap {
	address := accounts.NormalizeEmail(email)
	return []db.LoginFailureCap{
		{Key: failureKey("password-from", address+"\n"+ClientIPFromRequest(r)), Limit: loginFailureLimit},
		{Key: failureKey("password", address), Limit: loginFailureAccountLimit},
	}
}

// setupKeyFailureCaps count wrong setup keys from one address.
func setupKeyFailureCaps(r *http.Request) []db.LoginFailureCap {
	return []db.LoginFailureCap{{Key: failureKey("setup-key", ClientIPFromRequest(r)), Limit: loginFailureLimit}}
}

func failureKeys(caps []db.LoginFailureCap) []string {
	keys := make([]string, len(caps))
	for i, limit := range caps {
		keys[i] = limit.Key
	}
	return keys
}

// lockedOut answers 429 when a cap is full, and 503 when the counts can't be read, since
// a lockout that can't be checked must not let guesses through. It reports whether it
// answered.
func lockedOut(w http.ResponseWriter, r *http.Request, store loginFailureStore, caps []db.LoginFailureCap, message string) bool {
	blocked, retryAfter, err := store.LoginFailuresBlocked(r.Context(), caps, loginFailureWindow)
	if err != nil {
		log.Printf("sign-in failures not read: %v", err)
		http.Error(w, "authentication unavailable", http.StatusServiceUnavailable)
		return true
	}
	if !blocked {
		return false
	}
	w.Header().Set("Retry-After", strconv.Itoa(retryAfterSeconds(retryAfter)))
	http.Error(w, message, http.StatusTooManyRequests)
	return true
}

func recordFailure(r *http.Request, store loginFailureStore, caps []db.LoginFailureCap) {
	if err := store.RecordLoginFailure(r.Context(), failureKeys(caps), loginFailureWindow); err != nil {
		log.Printf("sign-in failure not recorded: %v", err)
	}
}

func clearFailures(r *http.Request, store loginFailureStore, caps []db.LoginFailureCap) {
	if err := store.ClearLoginFailures(r.Context(), failureKeys(caps)); err != nil {
		log.Printf("sign-in failures not cleared: %v", err)
	}
}
