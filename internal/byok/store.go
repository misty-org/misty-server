package byok

import (
	"context"
	"net/http"
	"sync"
	"time"
)

// TTL is how long the server keeps an account's credentials after its last
// request carried them. Background work (schedules, continuations) runs on
// this copy until it expires.
const TTL = 24 * time.Hour

// MaxAccounts bounds the store. When full, expired entries go first, then the
// least recently refreshed.
const MaxAccounts = 10_000

type entry struct {
	credentials *Credentials
	expires     time.Time
}

// Store keeps each account's latest credentials in memory. It is never
// persisted or logged.
type Store struct {
	mu      sync.Mutex
	entries map[string]entry
	now     func() time.Time
	ttl     time.Duration
	max     int
}

// Default is the process's store: the account middleware fills it and model
// admission reads it.
var Default = NewStore()

func NewStore() *Store {
	return &Store{entries: map[string]entry{}, now: time.Now, ttl: TTL, max: MaxAccounts}
}

// TestingStore is a store with its own clock and bound.
func TestingStore(now func() time.Time, max int) *Store {
	return &Store{entries: map[string]entry{}, now: now, ttl: TTL, max: max}
}

// Put records an account's credentials and restarts their TTL.
func (s *Store) Put(account string, c *Credentials) {
	if s == nil || account == "" || c == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	now := s.now()
	if _, ok := s.entries[account]; !ok && len(s.entries) >= s.max {
		s.evictLocked(now)
	}
	s.entries[account] = entry{credentials: c, expires: now.Add(s.ttl)}
}

// Get is the account's credentials while they have not expired.
func (s *Store) Get(account string) *Credentials {
	if s == nil || account == "" {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	e, ok := s.entries[account]
	if !ok {
		return nil
	}
	if !s.now().Before(e.expires) {
		delete(s.entries, account)
		return nil
	}
	return e.credentials
}

// Forget drops an account's credentials (sign-out, deletion).
func (s *Store) Forget(account string) {
	if s == nil {
		return
	}
	s.mu.Lock()
	delete(s.entries, account)
	s.mu.Unlock()
}

func (s *Store) evictLocked(now time.Time) {
	oldest, oldestAt := "", time.Time{}
	for account, e := range s.entries {
		if !now.Before(e.expires) {
			delete(s.entries, account)
			continue
		}
		if oldest == "" || e.expires.Before(oldestAt) {
			oldest, oldestAt = account, e.expires
		}
	}
	if len(s.entries) >= s.max && oldest != "" {
		delete(s.entries, oldest)
	}
}

// Len is how many accounts the store holds.
func (s *Store) Len() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.entries)
}

// For is the credentials for an account: the request's own when it carried
// them for that account, else the stored copy.
func (s *Store) For(ctx context.Context, account string) *Credentials {
	if c := FromContext(ctx); c != nil && accountFromContext(ctx) == account {
		return c
	}
	return s.Get(account)
}

type accountKey struct{}

func accountFromContext(ctx context.Context) string {
	account, _ := ctx.Value(accountKey{}).(string)
	return account
}

// Middleware decodes X-Misty-Models on requests from a signed-in account,
// puts the credentials in the request context and the store, and removes the
// header so nothing downstream can log or forward it. A malformed or
// oversized header is ignored. account names the request's account, or "".
func (s *Store) Middleware(account func(*http.Request) string) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			value := r.Header.Get(Header)
			if value == "" {
				next.ServeHTTP(w, r)
				return
			}
			r.Header.Del(Header)
			user := account(r)
			if user == "" {
				next.ServeHTTP(w, r)
				return
			}
			c, err := Decode(value)
			if err != nil {
				next.ServeHTTP(w, r)
				return
			}
			s.Put(user, c)
			ctx := context.WithValue(WithCredentials(r.Context(), c), accountKey{}, user)
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}
