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

func failureKey(kind, value string) string {
	sum := sha256.Sum256([]byte(kind + "\n" + value))
	return hex.EncodeToString(sum[:])
}

func passwordFailureCaps(r *http.Request, email string) []db.LoginFailureCap {
	address := accounts.NormalizeEmail(email)
	return []db.LoginFailureCap{
		{Key: failureKey("password-from", address+"\n"+ClientIPFromRequest(r)), Limit: loginFailureLimit},
		{Key: failureKey("password", address), Limit: loginFailureAccountLimit},
	}
}

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
