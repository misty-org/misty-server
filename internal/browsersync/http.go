package browsersync

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log"
	"net/http"
	"sync"
	"time"

	"github.com/kannachi323/misty/server/internal/accounts"
	"github.com/kannachi323/misty/server/internal/billingadapter"
	"github.com/kannachi323/misty/server/internal/platform/transport"

	"github.com/google/uuid"
	"github.com/kannachi323/misty/server/internal/platform/db"
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
	// batchPolicy is the base batching policy sent to batching clients.
	batchPolicy BatchPolicy
	// publishes limits how often each device may publish.
	publishes publishLimiter
}

const syncSessionRevalidation = 10 * time.Minute

func (s *BrowserSyncService) sessionRevalidation() time.Duration {
	if s.revalidateEvery > 0 {
		return s.revalidateEvery
	}
	return syncSessionRevalidation
}

func NewBrowserSyncService(database *db.Database) *BrowserSyncService {
	policy, err := batchPolicyFromEnv()
	if err != nil {
		log.Printf("browser sync batching uses its defaults: %v", err)
	}
	return &BrowserSyncService{database: database, store: NewStore(database.Conn), workspaces: newWorkspaceCaches(256 << 20), instanceID: uuid.NewString(), batchPolicy: policy}
}

func syncErrorCode(err error) (string, int) {
	switch {
	case errors.Is(err, billingadapter.ErrDenied):
		return "account_usage_limit_reached", 402
	case errors.Is(err, ErrSyncInvalid):
		return "invalid_sync_request", 400
	case errors.Is(err, ErrSyncReauth):
		return "sync_reauthentication_failed", 412
	case errors.Is(err, ErrSyncRateLimited):
		return "sync_rate_limited", 429
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
// confirmPassword checks the password given to confirm deleting the vault.
func (s *BrowserSyncService) confirmPassword(userID, password string) error {
	account, err := s.database.GetUserByID(userID)
	if err != nil {
		return err
	}
	if account == nil {
		return ErrSyncForbidden
	}
	var hash string
	if account.Provider == "misty" {
		if _, hash, err = s.database.GetUserByEmail(account.Email); err != nil {
			return err
		}
	}
	return confirmPassword(reauthFailures, userID, account.Provider, hash, password, time.Now())
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
		if r.Method == http.MethodDelete {
			// Deleting the vault destroys every device's synced data, so a session alone is
			// not enough: the person confirms with their password (a derived credential in
			// the desktop app).
			var body struct {
				CurrentPassword string `json:"current_password"`
			}
			if err := decodeSync(http.MaxBytesReader(w, r.Body, 4096), &body); err != nil {
				writeSyncError(w, err)
				return
			}
			if err := s.confirmPassword(user, body.CurrentPassword); err != nil {
				writeSyncError(w, err)
				return
			}
			// An account holds one vault at a time; deleting it is how a new one is made.
			deleted, err := s.store.DeleteBrowserSyncVaults(r.Context(), user)
			if err != nil {
				writeSyncError(w, err)
				return
			}
			transport.WriteJSON(w, 200, map[string]bool{"deleted": deleted})
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
		if err := s.store.CreateBrowserSyncVault(r.Context(), user, "", body.RootPublicKey, PasswordVaultKeys(body.KeyEnvelope), body.Device); err != nil {
			writeSyncError(w, err)
			return
		}
		transport.WriteJSON(w, 201, map[string]string{"vault_id": body.Device.VaultID})
	}
}

// VaultKey replaces the personal vault's password wrapper (PUT). The body
// carries the new wrapper and a transition signed by the vault root.
func (s *BrowserSyncService) VaultKey() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		user, ok := s.user(w, r)
		if !ok {
			return
		}
		var body struct {
			KeyEnvelope   SyncKeyEnvelope   `json:"key_envelope"`
			KeyTransition SyncKeyTransition `json:"key_transition"`
		}
		if err := decodeSync(http.MaxBytesReader(w, r.Body, 16384), &body); err != nil {
			writeSyncError(w, err)
			return
		}
		if !keyRotations.note(user, time.Now()) {
			writeSyncError(w, ErrSyncRateLimited)
			return
		}
		vault, err := s.store.RotateBrowserSyncVaultKey(r.Context(), user, body.KeyEnvelope, body.KeyTransition)
		if err != nil {
			writeSyncError(w, err)
			return
		}
		transport.WriteJSON(w, 200, map[string]any{"vault": vault})
	}
}

// Vaults lists the account's vaults, one per Space, and creates one for a
// Space the caller belongs to.
func (s *BrowserSyncService) Vaults() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		user, ok := s.user(w, r)
		if !ok {
			return
		}
		if r.Method == http.MethodGet {
			vaults, err := s.store.BrowserSyncVaults(r.Context(), user)
			if err != nil {
				writeSyncError(w, err)
				return
			}
			transport.WriteJSON(w, 200, map[string]any{"vaults": vaults})
			return
		}
		var body struct {
			SpaceID       string          `json:"space_id"`
			RootPublicKey []byte          `json:"root_public_key"`
			Device        SyncDeviceGrant `json:"device"`
			SyncVaultKeys
		}
		if err := decodeSync(http.MaxBytesReader(w, r.Body, 16384), &body); err != nil {
			writeSyncError(w, err)
			return
		}
		if body.SpaceID == "" || len(body.SpaceID) > 128 {
			writeSyncError(w, ErrSyncInvalid)
			return
		}
		if err := s.store.CreateBrowserSyncVault(r.Context(), user, body.SpaceID, body.RootPublicKey, body.SyncVaultKeys, body.Device); err != nil {
			writeSyncError(w, err)
			return
		}
		transport.WriteJSON(w, 201, map[string]string{"vault_id": body.Device.VaultID, "space_id": body.SpaceID})
	}
}
func (s *BrowserSyncService) Devices() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		user, ok := s.user(w, r)
		if !ok {
			return
		}
		if r.Method == http.MethodGet {
			// Without a vault_id this lists the personal vault's devices.
			var vault *SyncVault
			var err error
			if id := r.URL.Query().Get("vault_id"); id != "" {
				vault, err = s.store.BrowserSyncVaultByID(r.Context(), user, id)
			} else {
				vault, err = s.store.BrowserSyncVault(r.Context(), user)
			}
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
