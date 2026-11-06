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

	"github.com/go-chi/chi/v5"
	"github.com/misty-org/misty-server/internal/accounts"
	"github.com/misty-org/misty-server/internal/platform/db"
	"github.com/misty-org/misty-server/internal/platform/security"
	"github.com/misty-org/misty-server/internal/platform/transport"
)

// BrowserSyncService serves the vault. It never holds a key and can't read anything it stores.
type BrowserSyncService struct {
	database *db.Database
	store    *Store
	scopeMu  sync.Mutex
	scope    context.Context
	endScope context.CancelFunc
	// revalidateEvery bounds how long a socket outlives a revoked account login; zero means
	// syncSessionRevalidation.
	revalidateEvery time.Duration
	batchPolicy     BatchPolicy
	publishes       publishLimiter
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
	return &BrowserSyncService{database: database, store: NewStore(database.Conn), batchPolicy: policy}
}

// connectionScope ends with Shutdown, closing every socket.
func (s *BrowserSyncService) connectionScope() context.Context {
	s.scopeMu.Lock()
	defer s.scopeMu.Unlock()
	if s.scope == nil {
		s.scope, s.endScope = context.WithCancel(context.Background())
	}
	return s.scope
}

// Shutdown closes every socket; clients reconnect to another process.
func (s *BrowserSyncService) Shutdown() {
	s.connectionScope()
	s.scopeMu.Lock()
	defer s.scopeMu.Unlock()
	s.endScope()
}

func syncErrorCode(err error) (string, int) {
	switch {
	case errors.Is(err, ErrSyncInvalid):
		return "invalid_sync_request", 400
	case errors.Is(err, ErrSyncReauth):
		return "sync_reauthentication_failed", 412
	case errors.Is(err, ErrSyncRateLimited):
		return "sync_rate_limited", 429
	case errors.Is(err, ErrSyncForbidden):
		return "sync_session_forbidden", 403
	case errors.Is(err, ErrSyncExists):
		return "sync_vault_exists", 409
	case errors.Is(err, ErrSyncStorageLimit):
		return "storage_limit_reached", 409
	case errors.Is(err, db.ErrAccountClosing):
		return "account_closing", 403
	default:
		return "sync_unavailable", 503
	}
}

func writeSyncError(w http.ResponseWriter, err error) {
	code, status := syncErrorCode(err)
	if status == 503 {
		log.Printf("browser sync: %v", err)
	}
	transport.WriteJSON(w, status, map[string]string{"code": code})
}

func decodeSync(r io.Reader, target any) error {
	decoder := json.NewDecoder(r)
	if decoder.Decode(target) != nil || decoder.Decode(&struct{}{}) != io.EOF {
		return ErrSyncInvalid
	}
	return nil
}

func (s *BrowserSyncService) user(w http.ResponseWriter, r *http.Request) (string, bool) {
	w.Header().Set("Cache-Control", "no-store")
	return accounts.AuthenticatedUser(w, r)
}

// confirmDestroy checks one of the proofs destroying a vault accepts: the vault root's signature
// from an open session; or, for someone who has lost the vault password, the account password, or
// for an account with no password a sign-in made in the last few minutes.
func (s *BrowserSyncService) confirmDestroy(ctx context.Context, r *http.Request, userID, password string, proof []byte) error {
	if len(proof) > 0 {
		ok, err := s.store.VerifyDestroyProof(ctx, userID, proof)
		if err != nil {
			return err
		}
		if !ok {
			return ErrSyncReauth
		}
		return nil
	}
	account, err := s.database.GetUserByID(userID)
	if err != nil {
		return err
	}
	if account == nil {
		return ErrSyncForbidden
	}
	if account.Provider != "misty" {
		fresh, err := s.database.AccountSessionFresh(ctx, security.HashToken(accounts.SessionID(r)), userID, syncReauthWindow)
		if err != nil {
			return err
		}
		if !fresh {
			return ErrSyncReauth
		}
		return nil
	}
	_, hash, err := s.database.GetUserByEmail(account.Email)
	if err != nil {
		return err
	}
	return confirmPassword(reauthFailures, userID, hash, password, time.Now())
}

// Vault reads, creates and destroys the account's vault.
func (s *BrowserSyncService) Vault() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		user, ok := s.user(w, r)
		if !ok {
			return
		}
		switch r.Method {
		case http.MethodGet:
			vault, err := s.store.Vault(r.Context(), user)
			if err != nil {
				writeSyncError(w, err)
				return
			}
			transport.WriteJSON(w, 200, map[string]any{"vault": vault})
		case http.MethodDelete:
			var body struct {
				CurrentPassword string `json:"current_password"`
				Proof           []byte `json:"proof"`
			}
			if err := decodeSync(http.MaxBytesReader(w, r.Body, 4096), &body); err != nil {
				writeSyncError(w, err)
				return
			}
			if err := s.confirmDestroy(r.Context(), r, user, body.CurrentPassword, body.Proof); err != nil {
				writeSyncError(w, err)
				return
			}
			deleted, err := s.store.DeleteVault(r.Context(), user)
			if err != nil {
				writeSyncError(w, err)
				return
			}
			transport.WriteJSON(w, 200, map[string]bool{"deleted": deleted})
		default:
			var body struct {
				RootPublicKey []byte           `json:"root_public_key"`
				KeyEnvelope   SyncKeyEnvelope  `json:"key_envelope"`
				Session       SyncSessionGrant `json:"session"`
				Name          string           `json:"name"`
				Platform      string           `json:"platform"`
				OSVersion     string           `json:"os_version"`
			}
			if err := decodeSync(http.MaxBytesReader(w, r.Body, 16384), &body); err != nil {
				writeSyncError(w, err)
				return
			}
			if err := s.store.CreateVault(r.Context(), user, body.RootPublicKey, body.KeyEnvelope, body.Session, body.Name, body.Platform, body.OSVersion); err != nil {
				writeSyncError(w, err)
				return
			}
			transport.WriteJSON(w, 201, map[string]string{"vault_id": body.Session.VaultID})
		}
	}
}

// VaultKey replaces the sealed root (PUT): the new wrapper, and a transition signed by the root.
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
		vault, err := s.store.RotateVaultKey(r.Context(), user, body.KeyEnvelope, body.KeyTransition)
		if err != nil {
			writeSyncError(w, err)
			return
		}
		transport.WriteJSON(w, 200, map[string]any{"vault": vault})
	}
}

// Sessions lists the vault's signed-in sessions (GET) and admits one with a root-signed grant (POST).
func (s *BrowserSyncService) Sessions() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		user, ok := s.user(w, r)
		if !ok {
			return
		}
		if r.Method == http.MethodGet {
			vault, err := s.store.Vault(r.Context(), user)
			if err != nil {
				writeSyncError(w, err)
				return
			}
			if vault == nil {
				writeSyncError(w, ErrSyncForbidden)
				return
			}
			sessions, err := s.store.Sessions(r.Context(), user, vault.VaultID)
			if err != nil {
				writeSyncError(w, err)
				return
			}
			transport.WriteJSON(w, 200, map[string]any{"sessions": sessions})
			return
		}
		var body struct {
			Session   SyncSessionGrant `json:"session"`
			Name      string           `json:"name"`
			Platform  string           `json:"platform"`
			OSVersion string           `json:"os_version"`
		}
		if err := decodeSync(http.MaxBytesReader(w, r.Body, 8192), &body); err != nil {
			writeSyncError(w, err)
			return
		}
		if err := s.store.EnrollSession(r.Context(), user, body.Session, body.Name, body.Platform, body.OSVersion); err != nil {
			writeSyncError(w, err)
			return
		}
		transport.WriteJSON(w, 201, map[string]string{"session_id": body.Session.SessionID})
	}
}

// SessionName renames a signed-in session (PUT).
func (s *BrowserSyncService) SessionName() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		user, ok := s.user(w, r)
		if !ok {
			return
		}
		var body struct {
			Name string `json:"name"`
		}
		if err := decodeSync(http.MaxBytesReader(w, r.Body, 1024), &body); err != nil {
			writeSyncError(w, err)
			return
		}
		if err := s.store.RenameSession(r.Context(), user, chi.URLParam(r, "sessionID"), body.Name); err != nil {
			writeSyncError(w, err)
			return
		}
		w.WriteHeader(204)
	}
}

// SessionRemove revokes a signed-in session (POST), with the vault root's signature.
func (s *BrowserSyncService) SessionRemove() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		user, ok := s.user(w, r)
		if !ok {
			return
		}
		var body struct {
			Proof []byte `json:"proof"`
		}
		if err := decodeSync(http.MaxBytesReader(w, r.Body, 1024), &body); err != nil {
			writeSyncError(w, err)
			return
		}
		if err := s.store.RemoveSession(r.Context(), user, chi.URLParam(r, "sessionID"), body.Proof); err != nil {
			writeSyncError(w, err)
			return
		}
		w.WriteHeader(204)
	}
}

// Usage reports what the vault takes up, per opaque scope.
func (s *BrowserSyncService) Usage() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		user, ok := s.user(w, r)
		if !ok {
			return
		}
		usage, err := s.store.Usage(r.Context(), user)
		if err != nil {
			writeSyncError(w, err)
			return
		}
		transport.WriteJSON(w, 200, usage)
	}
}

// Ticket mints a one-use socket ticket for a session.
func (s *BrowserSyncService) Ticket() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		user, ok := s.user(w, r)
		if !ok {
			return
		}
		var body struct {
			VaultID   string `json:"vault_id"`
			SessionID string `json:"session_id"`
		}
		if err := decodeSync(http.MaxBytesReader(w, r.Body, 4096), &body); err != nil {
			writeSyncError(w, err)
			return
		}
		token, err := security.GenerateSecureToken()
		if err != nil {
			writeSyncError(w, err)
			return
		}
		login := ""
		if sid := accounts.SessionID(r); sid != "" {
			login = security.HashToken(sid)
		}
		if err = s.store.CreateTicket(r.Context(), user, body.VaultID, body.SessionID, security.HashToken(token), login); err != nil {
			writeSyncError(w, err)
			return
		}
		transport.WriteJSON(w, 201, map[string]any{"ticket": token, "expires_in": 60})
	}
}
