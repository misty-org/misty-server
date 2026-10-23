package browsersync

import (
	"errors"
	"testing"
	"time"

	"golang.org/x/crypto/bcrypt"
)

func TestConfirmingADeletionNeedsThePasswordAndLimitsGuesses(t *testing.T) {
	hash, err := bcrypt.GenerateFromPassword([]byte("derived-credential"), bcrypt.MinCost)
	if err != nil {
		t.Fatal(err)
	}
	limiter := newAttemptLimiter(3, time.Minute)
	now := time.Now()
	check := func(password string, at time.Time) error {
		return confirmPassword(limiter, "account", "misty", string(hash), password, at)
	}
	for i := 0; i < 3; i++ {
		if err := check("a guess", now); !errors.Is(err, ErrSyncReauth) {
			t.Fatalf("a wrong password was accepted: %v", err)
		}
	}
	// Past the limit even the right password waits, so guesses cannot be tried fast.
	if err := check("derived-credential", now); !errors.Is(err, ErrSyncRateLimited) {
		t.Fatalf("the limit did not hold: %v", err)
	}
	// After the window the right password works and clears the count.
	later := now.Add(2 * time.Minute)
	if err := check("derived-credential", later); err != nil {
		t.Fatalf("the right password was refused: %v", err)
	}
	if err := check("a guess", later); !errors.Is(err, ErrSyncReauth) {
		t.Fatalf("the count was not cleared: %v", err)
	}
	// An account with no password (Google) is not asked for one.
	if err := confirmPassword(limiter, "google", "google", "", "", later); err != nil {
		t.Fatalf("an account without a password was refused: %v", err)
	}
}

func TestKeyRotationIsRateLimitedPerAccount(t *testing.T) {
	limiter := newAttemptLimiter(2, time.Minute)
	now := time.Now()
	if !limiter.note("a", now) || !limiter.note("a", now) {
		t.Fatal("attempts within the limit were refused")
	}
	if limiter.note("a", now) {
		t.Fatal("an attempt past the limit was allowed")
	}
	if !limiter.note("b", now) {
		t.Fatal("one account's attempts limited another's")
	}
	if !limiter.note("a", now.Add(2*time.Minute)) {
		t.Fatal("the limit did not lapse")
	}
}

func TestReauthenticationAndRateLimitErrorsHaveTheirOwnCodes(t *testing.T) {
	if code, status := syncErrorCode(ErrSyncReauth); code != "sync_reauthentication_failed" || status != 412 {
		t.Fatalf("a wrong password maps to %s %d", code, status)
	}
	if code, status := syncErrorCode(ErrSyncRateLimited); code != "sync_rate_limited" || status != 429 {
		t.Fatalf("a rate limit maps to %s %d", code, status)
	}
}
