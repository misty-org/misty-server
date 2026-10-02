package browsersync

import (
	"context"
	"log"
	"time"
)

// RunLiveness renews this process's connection lease and removes connections
// left by processes whose lease lapsed. It is the only periodic sync write: one
// statement batch per API process every SyncInstanceLease/3, however many
// devices are connected. Sockets keep transport pings, which touch no database.
func (s *BrowserSyncService) RunLiveness(ctx context.Context) {
	renewed := time.Now()
	renew := func() {
		// Another process may sweep this one's rows once the lease lapses. Close
		// the sockets first, so no open socket is left without its row; clients
		// reconnect and register again under the renewed lease.
		if time.Since(renewed) >= SyncInstanceLease-5*time.Second {
			s.fenceConnections()
		}
		started := time.Now()
		bounded, cancel := context.WithTimeout(ctx, 5*time.Second)
		defer cancel()
		if _, err := s.store.RenewBrowserSyncInstance(bounded, s.instanceID); err != nil {
			if ctx.Err() == nil {
				log.Printf("browser sync lease renewal failed: %v", err)
			}
			return
		}
		renewed = started
	}
	renew()
	ticker := time.NewTicker(SyncInstanceLease / 3)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			// Devices on this process go offline now, not when the lease lapses.
			release, cancel := context.WithTimeout(context.WithoutCancel(ctx), 3*time.Second)
			_ = s.store.ReleaseBrowserSyncInstance(release, s.instanceID)
			cancel()
			return
		case <-ticker.C:
			renew()
		}
	}
}

// connectionScope ends when this process fences its connections.
func (s *BrowserSyncService) connectionScope() context.Context {
	s.scopeMu.Lock()
	defer s.scopeMu.Unlock()
	if s.scope == nil {
		s.scope, s.endScope = context.WithCancel(context.Background())
	}
	return s.scope
}

func (s *BrowserSyncService) fenceConnections() {
	s.scopeMu.Lock()
	defer s.scopeMu.Unlock()
	if s.endScope != nil {
		s.endScope()
	}
	s.scope, s.endScope = context.WithCancel(context.Background())
}
