package browsersync

import (
	"context"
	"math/rand/v2"
	"time"

	"github.com/google/uuid"
	"github.com/gorilla/websocket"
)

func (s *BrowserSyncService) serveConnection(parent context.Context, conn *websocket.Conn, identity SyncConnectionIdentity, after int64, version int, batching bool) {
	ctx, cancel := context.WithCancel(parent)
	defer cancel()
	// A fenced process closes every socket, including this one.
	defer context.AfterFunc(s.connectionScope(), cancel)()
	connectionID := uuid.NewString()
	if s.store.BrowserSyncConnect(ctx, identity, connectionID, s.instanceID, after, false) != nil {
		return
	}
	defer func() {
		cleanup, stop := context.WithTimeout(context.Background(), 3*time.Second)
		defer stop()
		_ = s.store.BrowserSyncDisconnect(cleanup, identity, connectionID)
	}()
	events, unsubscribe, err := s.database.SubscribeAccountEvents(ctx, identity.UserID)
	if err != nil {
		return
	}
	defer unsubscribe()
	outgoing := make(chan any, 32)
	resume := make(chan int64, 1)
	var watches chan workspaceWatch
	if version == syncWorkspaceProtocol {
		watches = make(chan workspaceWatch, 8)
	}
	done := make(chan struct{})
	send := func(frame any) bool {
		select {
		case outgoing <- frame:
			return true
		case <-ctx.Done():
			return false
		default:
			cancel()
			_ = conn.Close()
			return false
		}
	}
	go func() {
		defer close(done)
		defer cancel()
		defer conn.Close()
		s.writeConnection(ctx, conn, identity, connectionID, after, events, outgoing, resume, watches, batching)
	}()
	defer func() { cancel(); _ = conn.Close(); <-done }()
	reader := newSyncReader(ctx, s, conn, identity, connectionID, after, version, batching, send, resume, watches)
	reader.start()
	reader.run()
}

// jittered spreads d uniformly across ±spread of itself.
func jittered(d time.Duration, spread float64) time.Duration {
	return time.Duration(float64(d) * (1 - spread + 2*spread*rand.Float64()))
}
