package api

import (
	"context"
	"log"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/misty-org/misty-server/internal/accounts"
)

const suspensionRefresh = 15 * time.Second

type suspendedAccountStore interface {
	SuspendedAccounts(context.Context) ([]string, error)
	ClosingAccounts(context.Context) (map[string]time.Time, error)
}

type SuspensionGuard struct {
	store   suspendedAccountStore
	now     func() time.Time
	mu      sync.Mutex
	ids     map[string]bool
	closing map[string]time.Time
	loaded  time.Time
}

var suspensions *SuspensionGuard

func NewSuspensionGuard(store suspendedAccountStore) *SuspensionGuard {
	guard := &SuspensionGuard{store: store, now: time.Now, ids: map[string]bool{}, closing: map[string]time.Time{}}
	suspensions = guard
	return guard
}

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

func (g *SuspensionGuard) Suspended(ctx context.Context, userID string) bool {
	if g == nil || userID == "" {
		return false
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	g.reload(ctx)
	return g.ids[userID]
}

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

func (g *SuspensionGuard) MarkClosing(userID string, deleteAt time.Time) {
	if g == nil {
		return
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	g.closing[userID] = deleteAt.UTC()
}

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

const accountSuspendedMessage = "This account is suspended. Its data is kept; contact the server's operator."

func writeAccountSuspended(w http.ResponseWriter) {
	writeJSON(w, http.StatusForbidden, map[string]string{"code": "account_suspended", "message": accountSuspendedMessage})
}

func writeSuspendedSignIn(w http.ResponseWriter, userID string) {
	body := map[string]string{"code": "account_suspended", "message": accountSuspendedMessage}
	if proof, err := newAccountProof(userID, time.Now()); err == nil {
		body["proof"] = proof
	} else {
		log.Printf("no proof for a suspended sign-in: %v", err)
	}
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusForbidden, body)
}
