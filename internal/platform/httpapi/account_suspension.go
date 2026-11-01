package api

import (
	"context"
	"log"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/kannachi323/misty/server/internal/accounts"
)

// suspensionRefresh is how stale the suspended and closing lists may get.
// Suspend, close and resume through the operator API on this instance apply at
// once; another instance picks them up within this long. Suspending also ends
// every session, so no access token outlives security.AccessTokenTTL anyway.
const suspensionRefresh = 15 * time.Second

type suspendedAccountStore interface {
	SuspendedAccounts(context.Context) ([]string, error)
	ClosingAccounts(context.Context) (map[string]time.Time, error)
}

// SuspensionGuard refuses every request of a suspended account with 403
// account_suspended, and every change from a closing account with 403
// account_closing (account_read_only.go). Either way the data is untouched.
type SuspensionGuard struct {
	store   suspendedAccountStore
	now     func() time.Time
	mu      sync.Mutex
	ids     map[string]bool
	closing map[string]time.Time
	loaded  time.Time
}

// suspensions is the process's guard; MCP runtime calls and collaboration
// tickets consult it too.
var suspensions *SuspensionGuard

// NewSuspensionGuard makes the process's guard.
func NewSuspensionGuard(store suspendedAccountStore) *SuspensionGuard {
	guard := &SuspensionGuard{store: store, now: time.Now, ids: map[string]bool{}, closing: map[string]time.Time{}}
	suspensions = guard
	return guard
}

// reload refreshes both lists when they are stale. A failed reload keeps the
// last ones. The caller holds g.mu.
func (g *SuspensionGuard) reload(ctx context.Context) {
	if g.now().Sub(g.loaded) <= suspensionRefresh {
		return
	}
	ids, err := g.store.SuspendedAccounts(ctx)
	if err != nil {
		log.Printf("suspended accounts could not be reloaded: %v", err)
	} else {
		g.ids = make(map[string]bool, len(ids))
		for _, id := range ids {
			g.ids[id] = true
		}
	}
	if closing, err := g.store.ClosingAccounts(ctx); err != nil {
		log.Printf("closing accounts could not be reloaded: %v", err)
	} else {
		g.closing = closing
	}
	g.loaded = g.now()
}

// Suspended reports whether the account is suspended.
func (g *SuspensionGuard) Suspended(ctx context.Context, userID string) bool {
	if g == nil || userID == "" {
		return false
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	g.reload(ctx)
	return g.ids[userID]
}

// Closing reports when a closing account is deleted, and whether it is
// closing at all.
func (g *SuspensionGuard) Closing(ctx context.Context, userID string) (time.Time, bool) {
	if g == nil || userID == "" {
		return time.Time{}, false
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	g.reload(ctx)
	at, ok := g.closing[userID]
	return at, ok
}

// Mark applies a suspend or resume this instance just made. Resuming also
// ends closing.
func (g *SuspensionGuard) Mark(userID string, suspended bool) {
	if g == nil {
		return
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	if suspended {
		g.ids[userID] = true
	} else {
		delete(g.ids, userID)
		delete(g.closing, userID)
	}
}

// MarkClosing applies a close this instance just made.
func (g *SuspensionGuard) MarkClosing(userID string, deleteAt time.Time) {
	if g == nil {
		return
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	g.closing[userID] = deleteAt.UTC()
}

// Middleware refuses a signed-in request from a suspended account, and a
// change from a closing one. Signing out still works, so the app can clear
// its session; a closing account also reads, refreshes and exports.
func (g *SuspensionGuard) Middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/logout") {
			next.ServeHTTP(w, r)
			return
		}
		user, _ := accounts.SessionUserID(r)
		if user != "" && g.Suspended(r.Context(), user) {
			writeAccountSuspended(w)
			return
		}
		if user != "" && !readOnlyRequest(r) {
			if deleteAt, closing := g.Closing(r.Context(), user); closing {
				writeAccountClosing(w, deleteAt)
				return
			}
		}
		next.ServeHTTP(w, r)
	})
}

func writeAccountSuspended(w http.ResponseWriter) {
	writeJSON(w, http.StatusForbidden, map[string]string{"code": "account_suspended", "message": "This account is suspended. Its data is kept; contact the server's operator."})
}
