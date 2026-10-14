package browsersync

import (
	"bytes"
	"context"
	"encoding/json"
	"time"

	"github.com/gorilla/websocket"
	"github.com/kannachi323/misty/server/internal/platform/metrics"
	db "github.com/kannachi323/misty/server/internal/platform/postgres"
)

// connectionWriter is the outbound half of one browser sync socket. It owns
// what the client has been sent so unchanged state is never resent.
type connectionWriter struct {
	service      *BrowserSyncService
	ctx          context.Context
	conn         *websocket.Conn
	identity     SyncConnectionIdentity
	connectionID string
	cursor       int64
	events       <-chan db.AccountEvent
	outgoing     <-chan any
	resume       <-chan int64
	watches      <-chan workspaceWatch
	batching     bool

	lastPolicy                                []byte
	lastDevices, lastPresence, lastWorkspaces []byte
	blocked                                   bool
	workspaces                                *workspacePusher
}

func (s *BrowserSyncService) writeConnection(ctx context.Context, conn *websocket.Conn, identity SyncConnectionIdentity, connectionID string, cursor int64, events <-chan db.AccountEvent, outgoing <-chan any, resume <-chan int64, watches <-chan workspaceWatch, batching bool) {
	w := &connectionWriter{
		service:      s,
		ctx:          ctx,
		conn:         conn,
		identity:     identity,
		connectionID: connectionID,
		cursor:       cursor,
		events:       events,
		outgoing:     outgoing,
		resume:       resume,
		watches:      watches,
		batching:     batching,
	}
	if !w.start() {
		return
	}
	// Transport keepalive only: pings keep proxies and NAT mappings open and
	// detect dead peers through the read deadline. They never touch the database.
	ping := time.NewTicker(15 * time.Second)
	defer ping.Stop()
	// Changes arrive by push. The account event hub turns a lost LISTEN session
	// or a slow consumer into a "reset", which replays authoritatively, so no
	// connection polls the database on a timer.
	//
	// The socket outlives the access token that minted its ticket. Instead of
	// reconnecting to re-present a credential, it confirms in band that the
	// minting account session is still active, on the same bound as before.
	revalidate := time.NewTimer(jittered(s.sessionRevalidation(), 0.25))
	defer revalidate.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-revalidate.C:
			if !w.revalidateSession(revalidate) {
				return
			}
		case next := <-resume:
			w.cursor = next
			w.blocked = false
			if w.replay() != nil {
				return
			}
		case frame := <-outgoing:
			if w.write(frame) != nil {
				return
			}
		case change := <-watches:
			if w.workspaces.watch(ctx, change) != nil {
				return
			}
		case event := <-events:
			if w.handleEvent(event) != nil {
				return
			}
		case <-ping.C:
			if conn.WriteControl(websocket.PingMessage, nil, time.Now().Add(10*time.Second)) != nil {
				return
			}
			metrics.RecordSyncMessage(ctx, "out", "ping", 0)
		}
	}
}

// start sends the welcome frame, the batching policy and the first replay.
// It returns false when the connection should end before it is serving.
func (w *connectionWriter) start() bool {
	vault, err := w.service.store.BrowserSyncVault(w.ctx, w.identity.UserID)
	if err != nil || vault == nil {
		return false
	}
	welcome := map[string]any{"type": "welcome", "vault": vault, "connection_id": w.connectionID}
	if w.watches != nil {
		welcome["protocol_version"] = syncWorkspaceProtocol
	}
	if w.write(welcome) != nil {
		return false
	}
	if w.policy() != nil {
		return false
	}
	w.workspaces = &workspacePusher{service: w.service, identity: w.identity, write: w.write, watched: map[string]int64{}}
	return w.replay() == nil
}

// write sends one frame, bounded by a write deadline.
func (w *connectionWriter) write(value any) error {
	if err := w.conn.SetWriteDeadline(time.Now().Add(10 * time.Second)); err != nil {
		return err
	}
	return writeSyncJSON(w.ctx, w.conn, value)
}

// policy resends the batching policy only when its lanes change.
func (w *connectionWriter) policy() error {
	if !w.batching {
		return nil
	}
	frame := w.service.batchPolicyFrame()
	lanes, _ := json.Marshal(frame["policy"].(map[string]any)["lanes"])
	if bytes.Equal(lanes, w.lastPolicy) {
		return nil
	}
	if err := w.write(frame); err != nil {
		return err
	}
	w.lastPolicy = lanes
	return nil
}

// replay pages through the change feed from the cursor until it is caught up.
func (w *connectionWriter) replay() error {
	if w.blocked {
		// A client awaiting a checkpoint still follows device changes.
		return w.presence()
	}
	if err := w.presence(); err != nil {
		return err
	}
	for {
		bounded, stop := context.WithTimeout(w.ctx, 5*time.Second)
		page, err := w.service.store.ReplayBrowserSync(bounded, w.identity.UserID, w.identity.VaultID, w.identity.DeviceID, w.cursor, 200)
		stop()
		if err != nil {
			return err
		}
		if page.CheckpointRequired {
			w.blocked = true
			return w.write(map[string]any{"type": "checkpoint_required", "head_sequence": page.HeadSequence})
		}
		if len(page.Events) == 0 {
			return nil
		}
		if err = w.write(map[string]any{"type": "events", "replay": page}); err != nil {
			return err
		}
		w.cursor = page.Events[len(page.Events)-1].Sequence
		if w.cursor >= page.HeadSequence {
			return nil
		}
		select {
		case <-w.ctx.Done():
			return w.ctx.Err()
		default:
		}
	}
}

// revalidateSession confirms the minting account session is still active and
// re-arms the timer. It returns false when the socket must close.
func (w *connectionWriter) revalidateSession(timer *time.Timer) bool {
	if w.identity.SessionHash == "" {
		// Tickets from before session binding renew by reconnecting.
		closeBrowserSyncForReconnect(w.conn)
		return false
	}
	bounded, stop := context.WithTimeout(w.ctx, 5*time.Second)
	active, err := w.service.database.AccountSessionActive(bounded, w.identity.SessionHash, w.identity.UserID)
	stop()
	if err == nil && !active {
		closeBrowserSyncForReconnect(w.conn)
		return false
	}
	next := jittered(w.service.sessionRevalidation(), 0.25)
	if err != nil {
		// A database outage must not disconnect every device at once.
		next = jittered(min(time.Minute, w.service.sessionRevalidation()), 0.5)
	}
	timer.Reset(next)
	return true
}

// handleEvent reacts to one account event and forwards the ones clients need.
func (w *connectionWriter) handleEvent(event db.AccountEvent) error {
	if event.Topic == "reset" || event.Topic == "browser-sync" {
		if err := w.replay(); err != nil {
			return err
		}
		if err := w.workspaces.refresh(w.ctx); err != nil {
			return err
		}
	}
	if event.Topic == "browser-presence" {
		if err := w.presence(); err != nil {
			return err
		}
	}
	if event.Topic == "reset" {
		if err := w.policy(); err != nil {
			return err
		}
	}
	// Every other topic, including "browser-records" (a cold collection
	// moved; the client pulls it when it wants), is a content-free hint
	// that older clients safely ignore.
	if event.Topic != "browser-sync" && event.Topic != "browser-presence" {
		event.UserID = ""
		return w.write(map[string]any{"type": "account_event", "event": event})
	}
	return nil
}
