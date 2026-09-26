package browsersync

import (
	"bytes"
	"crypto/ed25519"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"time"

	"github.com/kannachi323/misty/server/internal/accounts"
	"github.com/kannachi323/misty/server/internal/platform/transport"

	"github.com/gorilla/websocket"
	db "github.com/kannachi323/misty/server/internal/platform/postgres"
	"github.com/kannachi323/misty/server/internal/platform/security"
)

type BrowserSyncService struct {
	database *db.Database
	store    *Store
	trees    *treeCache
	restore  RestoreCompleter
}

func NewBrowserSyncService(database *db.Database) *BrowserSyncService {
	return &BrowserSyncService{database: database, store: NewStore(database.Conn), trees: newTreeCache(64 << 20)}
}

func syncErrorCode(err error) (string, int) {
	switch {
	case errors.Is(err, ErrSyncInvalid):
		return "invalid_sync_request", 400
	case errors.Is(err, ErrSyncForbidden):
		return "sync_device_forbidden", 403
	case errors.Is(err, ErrSyncExists):
		return "sync_workspace_exists", 409
	case errors.Is(err, ErrSyncEpoch):
		return "sync_key_epoch_changed", 409
	case errors.Is(err, ErrSyncCounterGap):
		return "sync_counter_gap", 409
	case errors.Is(err, ErrSyncOperationConflict):
		return "sync_operation_conflict", 409
	case errors.Is(err, ErrSyncCompacted):
		return "sync_operation_compacted", 409
	case errors.Is(err, ErrSyncCursor):
		return "sync_cursor_invalid", 409
	case errors.Is(err, ErrSyncTreeMode):
		return "sync_tree_mode", 426
	case errors.Is(err, ErrSyncTreeSnapshot):
		return "sync_tree_snapshot_required", 409
	default:
		return "sync_unavailable", 503
	}
}
func writeSyncError(w http.ResponseWriter, err error) {
	code, status := syncErrorCode(err)
	transport.WriteJSON(w, status, map[string]string{"code": code})
}
func decodeSync(r io.Reader, target any) error {
	decoder := json.NewDecoder(r)
	decoder.DisallowUnknownFields()
	if decoder.Decode(target) != nil || decoder.Decode(&struct{}{}) != io.EOF {
		return ErrSyncInvalid
	}
	return nil
}
func (s *BrowserSyncService) user(w http.ResponseWriter, r *http.Request) (string, bool) {
	w.Header().Set("Cache-Control", "no-store")
	user, ok := accounts.AuthenticatedUser(w, r)
	if !ok {
		return "", false
	}
	if db.AppAuthorityFromContext(r.Context()) != nil {
		writeSyncError(w, ErrSyncForbidden)
		return "", false
	}
	return user, true
}
func (s *BrowserSyncService) Workspace() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		user, ok := s.user(w, r)
		if !ok {
			return
		}
		if r.Method == http.MethodGet {
			workspace, err := s.store.BrowserSyncWorkspace(r.Context(), user)
			if err != nil {
				writeSyncError(w, err)
				return
			}
			transport.WriteJSON(w, 200, map[string]any{"workspace": workspace})
			return
		}
		var body struct {
			RootPublicKey []byte          `json:"root_public_key"`
			KeyEnvelope   SyncKeyEnvelope `json:"key_envelope"`
			Device        SyncDeviceGrant `json:"device"`
		}
		if err := decodeSync(http.MaxBytesReader(w, r.Body, 16384), &body); err != nil {
			writeSyncError(w, err)
			return
		}
		if err := s.store.CreateBrowserSyncWorkspace(r.Context(), user, body.RootPublicKey, body.KeyEnvelope, body.Device); err != nil {
			writeSyncError(w, err)
			return
		}
		transport.WriteJSON(w, 201, map[string]string{"workspace_id": body.Device.WorkspaceID})
	}
}
func (s *BrowserSyncService) Devices() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		user, ok := s.user(w, r)
		if !ok {
			return
		}
		if r.Method == http.MethodGet {
			workspace, err := s.store.BrowserSyncWorkspace(r.Context(), user)
			if err != nil {
				writeSyncError(w, err)
				return
			}
			if workspace == nil {
				writeSyncError(w, ErrSyncForbidden)
				return
			}
			devices, err := s.store.BrowserSyncDevices(r.Context(), user, workspace.WorkspaceID)
			if err != nil {
				writeSyncError(w, err)
				return
			}
			transport.WriteJSON(w, 200, map[string]any{"devices": devices})
			return
		}
		var body SyncDeviceGrant
		if err := decodeSync(http.MaxBytesReader(w, r.Body, 8192), &body); err != nil {
			writeSyncError(w, err)
			return
		}
		if err := s.store.EnrollBrowserSyncDevice(r.Context(), user, body); err != nil {
			writeSyncError(w, err)
			return
		}
		_ = s.store.NotifyBrowserSyncPresence(r.Context(), SyncConnectionIdentity{UserID: user, WorkspaceID: body.WorkspaceID})
		transport.WriteJSON(w, 201, map[string]string{"device_id": body.DeviceID})
	}
}
func (s *BrowserSyncService) Ticket() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		user, ok := s.user(w, r)
		if !ok {
			return
		}
		var body struct {
			WorkspaceID     string `json:"workspace_id"`
			DeviceID        string `json:"device_id"`
			ProtocolVersion int    `json:"protocol_version"`
		}
		if err := decodeSync(http.MaxBytesReader(w, r.Body, 4096), &body); err != nil {
			writeSyncError(w, err)
			return
		}
		if body.ProtocolVersion != 1 && body.ProtocolVersion != 2 {
			transport.WriteJSON(w, 426, map[string]string{"code": "sync_protocol_unsupported"})
			return
		}
		// A tree-protocol workspace no longer accepts legacy clients.
		if body.ProtocolVersion == 1 {
			if mode, err := s.store.BrowserSyncTreeMode(r.Context(), user, body.WorkspaceID); err == nil && mode {
				transport.WriteJSON(w, 426, map[string]string{"code": "sync_protocol_unsupported"})
				return
			}
		}
		token, err := security.GenerateSecureToken()
		if err != nil {
			writeSyncError(w, err)
			return
		}
		if err = s.store.CreateBrowserSyncTicket(r.Context(), user, body.WorkspaceID, body.DeviceID, security.HashToken(token)); err != nil {
			writeSyncError(w, err)
			return
		}
		transport.WriteJSON(w, 201, map[string]any{"ticket": token, "expires_in": 60})
	}
}

func syncConnectionProof(workspace, device, challenge string) []byte {
	raw, _ := json.Marshal([]any{"misty.sync.connect.v1", workspace, device, challenge})
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
	// Tree protocol (v2) fields.
	RequestID  string         `json:"request_id,omitempty"`
	TreeOp     *SyncTreeOp    `json:"tree_op,omitempty"`
	Claim      *SyncTreeClaim `json:"claim,omitempty"`
	TreeID     string         `json:"tree_id,omitempty"`
	TabNodeID  string         `json:"tab_node_id,omitempty"`
	Slot       int16          `json:"slot,omitempty"`
	Blob       *SyncBlob      `json:"blob,omitempty"`
	BlobHashes [][]byte       `json:"blob_hashes,omitempty"`
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
		if protocol != "" && protocol != "1" && protocol != "2" {
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
		if protocol == "2" {
			version = 2
		} else if mode, err := s.store.BrowserSyncTreeMode(r.Context(), identity.UserID, identity.WorkspaceID); err != nil {
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
		challenge, err := security.GenerateSecureToken()
		if err != nil {
			return
		}
		conn.SetReadLimit(4096)
		_ = conn.SetReadDeadline(time.Now().Add(10 * time.Second))
		_ = conn.SetWriteDeadline(time.Now().Add(10 * time.Second))
		if conn.WriteJSON(map[string]any{"type": "challenge", "protocol_version": version, "challenge": challenge}) != nil {
			return
		}
		kind, raw, err := conn.ReadMessage()
		if err != nil || kind != websocket.TextMessage {
			return
		}
		var auth syncClientFrame
		if decodeSync(bytes.NewReader(raw), &auth) != nil || auth.Type != "authenticate" || auth.After < 0 || auth.After > SyncMaxCounter || len(identity.PublicKey) != ed25519.PublicKeySize || !ed25519.Verify(identity.PublicKey, syncConnectionProof(identity.WorkspaceID, identity.DeviceID, challenge), auth.Signature) {
			_ = conn.WriteControl(websocket.CloseMessage, websocket.FormatCloseMessage(websocket.ClosePolicyViolation, "Device proof required"), time.Now().Add(time.Second))
			return
		}
		if version == 2 {
			// The first tree-protocol connection turns away legacy clients, which
			// would otherwise keep publishing the old shared workspace.
			if s.store.EnableBrowserSyncTreeMode(r.Context(), identity.UserID, identity.WorkspaceID) != nil {
				return
			}
		}
		s.serveConnection(r.Context(), conn, *identity, auth.After, version)
	}
}
