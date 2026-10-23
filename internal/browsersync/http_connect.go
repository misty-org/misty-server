package browsersync

import (
	"bytes"
	"crypto/ed25519"
	"encoding/json"
	"net/http"
	"strconv"
	"time"

	"github.com/kannachi323/misty/server/internal/platform/transport"

	"github.com/gorilla/websocket"
	"github.com/kannachi323/misty/server/internal/platform/metrics"
	"github.com/kannachi323/misty/server/internal/platform/security"
)

func syncConnectionProof(vault, device, challenge string) []byte {
	raw, _ := json.Marshal([]any{"misty.sync.connect.v1", vault, device, challenge})
	return raw
}

var browserSyncUpgrader = websocket.Upgrader{ReadBufferSize: 4096, WriteBufferSize: 4096,
	// A one-use authenticated ticket AND a fresh native-device signature are
	// required. No cookie-only browser upgrade can join this stream.
	CheckOrigin: func(*http.Request) bool { return true }, HandshakeTimeout: 10 * time.Second}

type syncClientFrame struct {
	Type            string        `json:"type"`
	Signature       []byte        `json:"signature,omitempty"`
	After           int64         `json:"after,omitempty"`
	Mutation        *SyncMutation `json:"mutation,omitempty"`
	AppliedSequence int64         `json:"applied_sequence,omitempty"`
	Ready           bool          `json:"ready,omitempty"`
	ActiveEpoch     string        `json:"active_epoch,omitempty"`
	Activation      *SyncMutation `json:"activation,omitempty"`
	// Workspace protocol (v2) fields.
	RequestID   string              `json:"request_id,omitempty"`
	WorkspaceOp *SyncWorkspaceOp    `json:"workspace_op,omitempty"`
	Claim       *SyncWorkspaceClaim `json:"claim,omitempty"`
	WorkspaceID string              `json:"workspace_id,omitempty"`
	ViewNodeID  string              `json:"view_node_id,omitempty"`
	Slot        int16               `json:"slot,omitempty"`
	Blob        *SyncBlob           `json:"blob,omitempty"`
	BlobHashes  [][]byte            `json:"blob_hashes,omitempty"`
	// Cold-tier records: `After` is the pull cursor.
	Collection string            `json:"collection,omitempty"`
	Writes     []SyncRecordWrite `json:"writes,omitempty"`
}

func (s *BrowserSyncService) Connect() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		if !websocket.IsWebSocketUpgrade(r) {
			w.Header().Set("Upgrade", "websocket")
			w.WriteHeader(426)
			return
		}
		token := r.URL.Query().Get("ticket")
		protocol := r.URL.Query().Get("protocol")
		// The client batches its edits and understands batch_policy and
		// sync_rate_limited. Only the workspace protocol batches.
		batching := r.URL.Query().Get("batching") == "1" && protocol == strconv.Itoa(syncWorkspaceProtocol)
		if protocol != "" && protocol != "1" && protocol != strconv.Itoa(syncWorkspaceProtocol) {
			transport.WriteJSON(w, 426, map[string]string{"code": "sync_protocol_unsupported"})
			return
		}
		if len(token) < 32 || len(token) > 128 {
			writeSyncError(w, ErrSyncForbidden)
			return
		}
		identity, err := s.store.ConsumeBrowserSyncTicket(r.Context(), security.HashToken(token))
		if err != nil {
			writeSyncError(w, err)
			return
		}
		version := 1
		if protocol == strconv.Itoa(syncWorkspaceProtocol) {
			version = syncWorkspaceProtocol
		} else if mode, err := s.store.BrowserSyncWorkspaceMode(r.Context(), identity.UserID, identity.VaultID); err != nil {
			writeSyncError(w, err)
			return
		} else if mode {
			transport.WriteJSON(w, 426, map[string]string{"code": "sync_protocol_unsupported"})
			return
		}
		conn, err := browserSyncUpgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer conn.Close()
		defer metrics.TrackSyncConnection(r.Context())()
		challenge, err := security.GenerateSecureToken()
		if err != nil {
			return
		}
		conn.SetReadLimit(4096)
		_ = conn.SetReadDeadline(time.Now().Add(10 * time.Second))
		_ = conn.SetWriteDeadline(time.Now().Add(10 * time.Second))
		if writeSyncJSON(r.Context(), conn, map[string]any{"type": "challenge", "protocol_version": version, "challenge": challenge}) != nil {
			return
		}
		kind, raw, err := conn.ReadMessage()
		if err != nil || kind != websocket.TextMessage {
			return
		}
		var auth syncClientFrame
		decodeErr := decodeSync(bytes.NewReader(raw), &auth)
		metrics.RecordSyncMessage(r.Context(), "in", auth.Type, len(raw))
		if decodeErr != nil || auth.Type != "authenticate" || auth.After < 0 || auth.After > SyncMaxCounter || len(identity.PublicKey) != ed25519.PublicKeySize || !ed25519.Verify(identity.PublicKey, syncConnectionProof(identity.VaultID, identity.DeviceID, challenge), auth.Signature) {
			_ = conn.WriteControl(websocket.CloseMessage, websocket.FormatCloseMessage(websocket.ClosePolicyViolation, "Device proof required"), time.Now().Add(time.Second))
			return
		}
		if version == syncWorkspaceProtocol {
			// The first workspace-protocol connection turns away legacy clients, which
			// would otherwise keep publishing the old shared vault.
			if s.store.EnableBrowserSyncWorkspaceMode(r.Context(), identity.UserID, identity.VaultID) != nil {
				return
			}
		}
		s.serveConnection(r.Context(), conn, *identity, auth.After, version, batching)
	}
}
