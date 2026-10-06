package browsersync

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strconv"
	"sync"
	"time"

	"github.com/kannachi323/misty/server/internal/accounts"
	"github.com/kannachi323/misty/server/internal/billingadapter"
	"github.com/kannachi323/misty/server/internal/platform/transport"

	"github.com/google/uuid"
	"github.com/gorilla/websocket"
	"github.com/kannachi323/misty/server/internal/platform/metrics"
	db "github.com/kannachi323/misty/server/internal/platform/postgres"
	"github.com/kannachi323/misty/server/internal/platform/security"
)

type BrowserSyncService struct {
	database   *db.Database
	store      *Store
	workspaces workspaceCaches
	restore    RestoreCompleter
	// instanceID owns this process's connection rows; they stay live while
	// RunLiveness renews its lease.
	instanceID string
	scopeMu    sync.Mutex
	scope      context.Context
	endScope   context.CancelFunc
	// revalidateEvery bounds how long a socket outlives a revoked account
	// session; zero means syncSessionRevalidation.
	revalidateEvery time.Duration
}

const syncSessionRevalidation = 10 * time.Minute

func (s *BrowserSyncService) sessionRevalidation() time.Duration {
	if s.revalidateEvery > 0 {
		return s.revalidateEvery
	}
	return syncSessionRevalidation
}

func NewBrowserSyncService(database *db.Database) *BrowserSyncService {
	return &BrowserSyncService{database: database, store: NewStore(database.Conn), workspaces: newWorkspaceCaches(256 << 20), instanceID: uuid.NewString()}
}

func syncErrorCode(err error) (string, int) {
	switch {
	case errors.Is(err, billingadapter.ErrDenied):
		return "account_usage_limit_reached", 402
	case errors.Is(err, ErrSyncInvalid):
		return "invalid_sync_request", 400
	case errors.Is(err, ErrSyncForbidden):
		return "sync_device_forbidden", 403
	case errors.Is(err, ErrSyncExists):
		return "sync_vault_exists", 409
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
	case errors.Is(err, ErrSyncWorkspaceMode):
		return "sync_workspace_mode", 426
	case errors.Is(err, ErrSyncWorkspaceSnapshot):
		return "sync_workspace_snapshot_required", 409
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
	return user, true
}
func (s *BrowserSyncService) Vault() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		user, ok := s.user(w, r)
		if !ok {
			return
		}
		if r.Method == http.MethodGet {
			vault, err := s.store.BrowserSyncVault(r.Context(), user)
			if err != nil {
				writeSyncError(w, err)
				return
			}
			transport.WriteJSON(w, 200, map[string]any{"vault": vault})
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
		if err := s.store.CreateBrowserSyncVault(r.Context(), user, body.RootPublicKey, body.KeyEnvelope, body.Device); err != nil {
			writeSyncError(w, err)
			return
		}
		transport.WriteJSON(w, 201, map[string]string{"vault_id": body.Device.VaultID})
	}
}
func (s *BrowserSyncService) Devices() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		user, ok := s.user(w, r)
		if !ok {
			return
		}
		if r.Method == http.MethodGet {
			vault, err := s.store.BrowserSyncVault(r.Context(), user)
			if err != nil {
				writeSyncError(w, err)
				return
			}
			if vault == nil {
				writeSyncError(w, ErrSyncForbidden)
				return
			}
			devices, err := s.store.BrowserSyncDevices(r.Context(), user, vault.VaultID)
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
			VaultID         string `json:"vault_id"`
			DeviceID        string `json:"device_id"`
			ProtocolVersion int    `json:"protocol_version"`
		}
		if err := decodeSync(http.MaxBytesReader(w, r.Body, 4096), &body); err != nil {
			writeSyncError(w, err)
			return
		}
		if body.ProtocolVersion != 1 && body.ProtocolVersion != syncWorkspaceProtocol {
			transport.WriteJSON(w, 426, map[string]string{"code": "sync_protocol_unsupported"})
			return
		}
		// A workspace-protocol vault no longer accepts legacy clients.
		if body.ProtocolVersion == 1 {
			if mode, err := s.store.BrowserSyncWorkspaceMode(r.Context(), user, body.VaultID); err == nil && mode {
				transport.WriteJSON(w, 426, map[string]string{"code": "sync_protocol_unsupported"})
				return
			}
		}
		token, err := security.GenerateSecureToken()
		if err != nil {
			writeSyncError(w, err)
			return
		}
		session := ""
		if sid := accounts.SessionID(r); sid != "" {
			session = security.HashToken(sid)
		}
		if err = s.store.CreateBrowserSyncTicket(r.Context(), user, body.VaultID, body.DeviceID, security.HashToken(token), session); err != nil {
			writeSyncError(w, err)
			return
		}
		transport.WriteJSON(w, 201, map[string]any{"ticket": token, "expires_in": 60})
	}
}

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
		s.serveConnection(r.Context(), conn, *identity, auth.After, version)
	}
}
