package api

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	. "github.com/kannachi323/misty/server/internal/platform/httpapi"

	db "github.com/kannachi323/misty/server/internal/platform/postgres"
)

func TestAbuseGuardBlockEscalatesThenRecovers(t *testing.T) {
	policy := AbusePolicy{
		TotalLimit: 100, TotalWindow: time.Minute,
		StrikesBeforeBlock: 2, StrikeWindow: time.Minute,
		BaseBlock: time.Minute, MaxBlock: 4 * time.Minute,
	}
	guard := NewAbuseGuard(policy)
	current := time.Now()
	guard.TestingNow = func() time.Time { return current }

	guard.RecordRejection("bad")
	guard.RecordRejection("bad")
	blocked, first := guard.Blocked("bad")
	if !blocked || first > time.Minute {
		t.Fatalf("first block = %v, want the one minute base", first)
	}

	// Re-offending after the block expires costs longer each time.
	current = current.Add(2 * time.Minute)
	guard.RecordRejection("bad")
	guard.RecordRejection("bad")
	if _, second := guard.Blocked("bad"); second <= time.Minute {
		t.Fatalf("second block = %v, want escalation beyond the base", second)
	}

	// And a caller that stops offending is eventually forgiven.
	current = current.Add(10 * time.Minute)
	if blocked, _ := guard.Blocked("bad"); blocked {
		t.Fatal("block should have expired")
	}
}

func TestAbuseGuardStateStaysBounded(t *testing.T) {
	guard := NewAbuseGuard(DefaultAbusePolicy())
	guard.TestingMaxKeys = 200
	for i := 0; i < 5000; i++ {
		guard.RecordRejection(fmt.Sprintf("caller-%d", i))
	}
	if tracked := guard.TrackedKeys(); tracked > 200 {
		t.Fatalf("tracked %d callers, want the cap of 200 to hold", tracked)
	}
}

func TestCostRoutesAreChargedPerAccountNotPerAddress(t *testing.T) {
	limiter := NewAPIRateLimiter()
	handler := hardeningHandler(limiter)

	allowed := 0
	for i := 0; i < 200; i++ {
		// One stolen credential, replayed from 200 different addresses — the
		// botnet case that per-IP limiting cannot see.
		request := httptest.NewRequest(http.MethodPost, "/ai/complete", nil)
		request.RemoteAddr = fmt.Sprintf("203.0.113.%d:1234", i%256)
		request.Header.Set("Authorization", "Bearer stolen-session-token")
		recorder := httptest.NewRecorder()
		handler.ServeHTTP(recorder, request)
		if recorder.Code == http.StatusOK {
			allowed++
		}
	}
	if allowed > 12 {
		t.Fatalf("one credential across many IPs got %d AI calls, want the 12/min account budget", allowed)
	}
}

func TestDistinctAccountsKeepIndependentBudgets(t *testing.T) {
	limiter := NewAPIRateLimiter()
	handler := hardeningHandler(limiter)

	call := func(token string) int {
		request := httptest.NewRequest(http.MethodPost, "/ai/complete", nil)
		request.RemoteAddr = "203.0.113.7:1234"
		request.Header.Set("Authorization", "Bearer "+token)
		recorder := httptest.NewRecorder()
		handler.ServeHTTP(recorder, request)
		return recorder.Code
	}
	for i := 0; i < 12; i++ {
		call("account-one")
	}
	// A second account sharing the same NAT address must not inherit the first
	// account's exhausted budget.
	if code := call("account-two"); code != http.StatusOK {
		t.Fatalf("second account behind the same address got %d, want 200", code)
	}
}

func TestUnauthenticatedTrafficStillKeysOnAddress(t *testing.T) {
	request := httptest.NewRequest(http.MethodPost, "/ai/complete", nil)
	request.RemoteAddr = "203.0.113.7:1234"
	if got := TestingRateLimitIdentity(request); got != "ip:203.0.113.7" {
		t.Fatalf("rateLimitIdentity() = %q, want the address for anonymous traffic", got)
	}
}

func TestIdentityKeyDoesNotLeakTheCredential(t *testing.T) {
	request := httptest.NewRequest(http.MethodPost, "/ai/complete", nil)
	request.Header.Set("Authorization", "Bearer super-secret-session-token")
	key := TestingRateLimitIdentity(request)
	if strings.Contains(key, "super-secret-session-token") {
		t.Fatalf("identity key %q embeds the raw credential", key)
	}
	if !TestingIdentityIsAccount(key) {
		t.Fatalf("identity key %q should be account-scoped", key)
	}
}

func TestBearerAndCookieCredentialsAreBothRecognised(t *testing.T) {
	bearer := httptest.NewRequest(http.MethodPost, "/ai/complete", nil)
	bearer.Header.Set("Authorization", "Bearer token-value")

	cookie := httptest.NewRequest(http.MethodPost, "/ai/complete", nil)
	cookie.AddCookie(&http.Cookie{Name: TestingSessionCookieName, Value: "token-value"})

	// The same session presented either way must land in the same bucket,
	// otherwise switching transport doubles the budget.
	if TestingRateLimitIdentity(bearer) != TestingRateLimitIdentity(cookie) {
		t.Fatal("bearer and cookie forms of one session produced different keys")
	}
}

// fakeBlockStore stands in for the database so the restart path can be tested
// without one.
type fakeBlockStore struct {
	mu     sync.Mutex
	blocks map[string]db.AbuseBlock
	fail   bool
}

func newFakeBlockStore() *fakeBlockStore {
	return &fakeBlockStore{blocks: map[string]db.AbuseBlock{}}
}

func (s *fakeBlockStore) SaveAbuseBlock(_ context.Context, block db.AbuseBlock) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if existing, ok := s.blocks[block.Key]; ok && existing.BlockSeconds > block.BlockSeconds {
		return nil
	}
	s.blocks[block.Key] = block
	return nil
}

func (s *fakeBlockStore) ActiveAbuseBlocks(context.Context) ([]db.AbuseBlock, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.fail {
		return nil, errors.New("database unavailable")
	}
	out := []db.AbuseBlock{}
	for _, block := range s.blocks {
		if block.BlockedUntil.After(time.Now()) {
			out = append(out, block)
		}
	}
	return out, nil
}

func (s *fakeBlockStore) count() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.blocks)
}

func TestBlocksSurviveARestart(t *testing.T) {
	store := newFakeBlockStore()
	policy := AbusePolicy{
		TotalLimit: 100, TotalWindow: time.Minute,
		StrikesBeforeBlock: 2, StrikeWindow: time.Minute,
		BaseBlock: 10 * time.Minute, MaxBlock: time.Hour,
	}

	first := NewAbuseGuard(policy).WithStore(context.Background(), store)
	first.RecordRejection("ip:203.0.113.7")
	first.RecordRejection("ip:203.0.113.7")
	if blocked, _ := first.Blocked("ip:203.0.113.7"); !blocked {
		t.Fatal("caller should be blocked before the restart")
	}
	// The write is asynchronous so a storage stall never delays a request.
	deadline := time.Now().Add(2 * time.Second)
	for store.count() == 0 && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}

	// A new process must not forgive the block simply because it restarted.
	second := NewAbuseGuard(policy).WithStore(context.Background(), store)
	if blocked, _ := second.Blocked("ip:203.0.113.7"); !blocked {
		t.Fatal("a restarted instance forgave an active block")
	}
}

func TestRefreshPropagatesBlocksBetweenInstances(t *testing.T) {
	store := newFakeBlockStore()
	policy := DefaultAbusePolicy()
	instanceB := NewAbuseGuard(policy).WithStore(context.Background(), store)

	// Another instance blocks a caller directly in shared storage.
	_ = store.SaveAbuseBlock(context.Background(), db.AbuseBlock{
		Key: "acct:abusive", BlockedUntil: time.Now().Add(15 * time.Minute), BlockSeconds: 900,
	})
	if blocked, _ := instanceB.Blocked("acct:abusive"); blocked {
		t.Fatal("instance B should not know about the block before refreshing")
	}
	instanceB.Refresh(context.Background())
	if blocked, _ := instanceB.Blocked("acct:abusive"); !blocked {
		t.Fatal("a block raised by another instance did not propagate")
	}
}

func TestRefreshFailureKeepsExistingBlocks(t *testing.T) {
	store := newFakeBlockStore()
	policy := AbusePolicy{
		TotalLimit: 100, TotalWindow: time.Minute,
		StrikesBeforeBlock: 1, StrikeWindow: time.Minute,
		BaseBlock: 10 * time.Minute, MaxBlock: time.Hour,
	}
	guard := NewAbuseGuard(policy).WithStore(context.Background(), store)
	guard.RecordRejection("ip:203.0.113.7")

	// A database blip must not silently unblock everyone.
	store.mu.Lock()
	store.fail = true
	store.mu.Unlock()
	guard.Refresh(context.Background())

	if blocked, _ := guard.Blocked("ip:203.0.113.7"); !blocked {
		t.Fatal("a failed refresh dropped an in-memory block")
	}
}

func TestGuardWithoutAStoreStillWorks(t *testing.T) {
	// Single-node runs and tests pass nil; the guard must stay fully functional.
	guard := NewAbuseGuard(DefaultAbusePolicy()).WithStore(context.Background(), nil)
	guard.Refresh(context.Background())
	if blocked, _ := guard.Blocked("ip:203.0.113.7"); blocked {
		t.Fatal("unexpected block")
	}
}
