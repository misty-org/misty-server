package browsersync

import (
	"context"
	"crypto/ed25519"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"errors"
	"regexp"
	"strconv"
	"time"

	"github.com/google/uuid"
)

var (
	ErrSyncInvalid   = errors.New("invalid sync request")
	ErrSyncForbidden = errors.New("sync session forbidden")
	ErrSyncExists    = errors.New("sync vault already exists")
	// ErrSyncStorageLimit refuses a write that adds content once the account
	// is at its storage limit. Deletions and reads still work.
	ErrSyncStorageLimit = errors.New("sync storage limit reached")
)

const SyncMaxCounter int64 = 9007199254740991

// The vault's root is sealed under a key the client derives from the vault password. The
// server never sees that password or that key, and can only check the wrapper's shape. Argon2
// parameters are named in the label so clients can raise them without a server change.
var syncKDFLabel = regexp.MustCompile(`^argon2id-m[0-9]{1,8}-t[0-9]{1,3}-p[0-9]{1,2}\.vault-v1$`)

// SyncKeyEnvelope is the sealed vault root.
type SyncKeyEnvelope struct {
	Version    int    `json:"version"`
	KDF        string `json:"kdf"`
	Salt       string `json:"salt"`
	Nonce      string `json:"nonce"`
	Ciphertext string `json:"ciphertext"`
}

func decodedLen(value string, min, max int) bool {
	raw, err := base64.StdEncoding.Strict().DecodeString(value)
	return err == nil && len(raw) >= min && len(raw) <= max
}

// Valid checks the wrapper's shape: nothing here can tell whether it opens.
func (e SyncKeyEnvelope) Valid() bool {
	return e.Version == 1 && syncKDFLabel.MatchString(e.KDF) &&
		decodedLen(e.Salt, 16, 64) && decodedLen(e.Nonce, 12, 12) && decodedLen(e.Ciphertext, 16, 4096)
}

// SyncKeyTransition authorizes replacing the sealed root. The vault root signs it, so only a
// session holding the open vault can make one; the server checks it, and every client checks it
// again against the root public key it pinned. Generations only rise by one.
type SyncKeyTransition struct {
	Generation int64  `json:"generation"`
	Signature  []byte `json:"signature"`
}

// digest covers every wrapper field, so a transition authorizes exactly one wrapper. It must
// match `KeyEnvelope::digest` in the native sync crate.
func (e SyncKeyEnvelope) digest() [32]byte {
	hash := sha256.New()
	hash.Write([]byte("misty.sync.key-envelope.v1"))
	for _, part := range []string{strconv.Itoa(e.Version), e.KDF, e.Salt, e.Nonce, e.Ciphertext} {
		hash.Write([]byte("\n"))
		hash.Write([]byte(part))
	}
	var sum [32]byte
	copy(sum[:], hash.Sum(nil))
	return sum
}

// keyTransitionSigningBytes must match `KeyTransition::signing_bytes` in the native sync crate.
func keyTransitionSigningBytes(vaultID string, generation int64, e SyncKeyEnvelope) []byte {
	digest := e.digest()
	data, _ := json.Marshal([]any{"misty.sync.key-transition.v1", vaultID, generation, base64.StdEncoding.EncodeToString(digest[:])})
	return data
}

func (t SyncKeyTransition) valid(root []byte, vaultID string, e SyncKeyEnvelope) bool {
	return t.Generation > 0 && t.Generation <= SyncMaxCounter &&
		len(root) == ed25519.PublicKeySize && len(t.Signature) == ed25519.SignatureSize &&
		ed25519.Verify(root, keyTransitionSigningBytes(vaultID, t.Generation, e), t.Signature)
}

// SyncVault is what a client needs to open the account's vault.
type SyncVault struct {
	VaultID          string             `json:"vault_id"`
	HeadSequence     int64              `json:"head_sequence"`
	CompactedThrough int64              `json:"compacted_through"`
	RootPublicKey    []byte             `json:"root_public_key"`
	KeyEnvelope      json.RawMessage    `json:"key_envelope"`
	KeyTransition    *SyncKeyTransition `json:"key_transition,omitempty"`
}

// SyncSessionGrant is a signed-in session's admission, signed by the vault root: account access
// alone can't add a session that clients would trust.
type SyncSessionGrant struct {
	VaultID   string `json:"vault_id"`
	SessionID string `json:"session_id"`
	PublicKey []byte `json:"public_key"`
	Signature []byte `json:"signature"`
}

// SigningBytes must match `SessionGrant::signing_bytes` in the native sync crate.
func (g SyncSessionGrant) SigningBytes() []byte {
	data, _ := json.Marshal([]any{"misty.sync.session.v1", g.VaultID, g.SessionID, base64.StdEncoding.EncodeToString(g.PublicKey)})
	return data
}

func validSyncID(id string) bool {
	v, err := uuid.Parse(id)
	return err == nil && v != uuid.Nil && v.String() == id
}

func (g SyncSessionGrant) valid(root []byte) bool {
	return validSyncID(g.VaultID) && validSyncID(g.SessionID) && len(root) == ed25519.PublicKeySize &&
		len(g.PublicKey) == ed25519.PublicKeySize && len(g.Signature) == ed25519.SignatureSize &&
		ed25519.Verify(root, g.SigningBytes(), g.Signature)
}

// SyncSession is one signed-in Misty install in the vault.
type SyncSession struct {
	SessionID  string     `json:"session_id"`
	Name       string     `json:"name"`
	Platform   string     `json:"platform"`
	OSVersion  string     `json:"os_version"`
	CreatedAt  time.Time  `json:"created_at"`
	LastSeenAt *time.Time `json:"last_seen_at"`
	RevokedAt  *time.Time `json:"revoked_at,omitempty"`
}

// Store owns the account's vault, its signed-in sessions and their records. Account
// authentication and event transport are supplied by the application.
type Store struct{ Conn *sql.DB }

func NewStore(conn *sql.DB) *Store { return &Store{Conn: conn} }

const syncVaultColumns = `vault_id::text,head_sequence,compacted_through,root_public_key,key_envelope,key_generation,key_transition_signature`

func scanSyncVault(row interface{ Scan(...any) error }) (*SyncVault, error) {
	var v SyncVault
	var key, transition []byte
	var generation int64
	if err := row.Scan(&v.VaultID, &v.HeadSequence, &v.CompactedThrough, &v.RootPublicKey, &key, &generation, &transition); err != nil {
		return nil, err
	}
	v.KeyEnvelope = key
	if generation > 0 {
		v.KeyTransition = &SyncKeyTransition{Generation: generation, Signature: transition}
	}
	return &v, nil
}

// Vault returns the account's vault, or nil when it has none.
func (db *Store) Vault(ctx context.Context, userID string) (*SyncVault, error) {
	v, err := scanSyncVault(db.Conn.QueryRowContext(ctx, `SELECT `+syncVaultColumns+` FROM sync_vaults WHERE user_id=$1`, userID))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	return v, err
}

func cleanSessionText(value string, limit int) string {
	runes := []rune(value)
	if len(runes) > limit {
		runes = runes[:limit]
	}
	return string(runes)
}

// CreateVault creates the account's one vault and admits the session that made it. Two sessions
// creating at once are safe: the second gets ErrSyncExists and opens the first's.
func (db *Store) CreateVault(ctx context.Context, userID string, root []byte, envelope SyncKeyEnvelope, grant SyncSessionGrant, name, platform, osVersion string) error {
	if !envelope.Valid() || !grant.valid(root) {
		return ErrSyncInvalid
	}
	wrapped, _ := json.Marshal(envelope)
	tx, err := db.Conn.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	result, err := tx.ExecContext(ctx, `INSERT INTO sync_vaults(vault_id,user_id,root_public_key,key_envelope) VALUES($1,$2,$3,$4) ON CONFLICT DO NOTHING`,
		grant.VaultID, userID, root, string(wrapped))
	if err != nil {
		return err
	}
	if n, err := result.RowsAffected(); err != nil {
		return err
	} else if n != 1 {
		return ErrSyncExists
	}
	if err = insertSession(ctx, tx, grant, name, platform, osVersion); err != nil {
		return err
	}
	return tx.Commit()
}

func insertSession(ctx context.Context, tx *sql.Tx, grant SyncSessionGrant, name, platform, osVersion string) error {
	_, err := tx.ExecContext(ctx, `INSERT INTO sync_sessions(vault_id,session_id,public_key,grant_signature,name,platform,os_version) VALUES($1,$2,$3,$4,$5,$6,$7)
		ON CONFLICT(vault_id,session_id) DO UPDATE SET name=excluded.name,platform=excluded.platform,os_version=excluded.os_version
		WHERE sync_sessions.public_key=excluded.public_key AND sync_sessions.revoked_at IS NULL`,
		grant.VaultID, grant.SessionID, grant.PublicKey, grant.Signature,
		cleanSessionText(name, 60), cleanSessionText(platform, 32), cleanSessionText(osVersion, 64))
	return err
}

// EnrollSession admits a session holding a grant signed by the vault root. A removed session's
// key stays refused; it joins again as a new session with a new key.
func (db *Store) EnrollSession(ctx context.Context, userID string, grant SyncSessionGrant, name, platform, osVersion string) error {
	if !validSyncID(grant.VaultID) || !validSyncID(grant.SessionID) {
		return ErrSyncInvalid
	}
	tx, err := db.Conn.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var root []byte
	err = tx.QueryRowContext(ctx, `SELECT root_public_key FROM sync_vaults WHERE user_id=$1 AND vault_id=$2 FOR SHARE`, userID, grant.VaultID).Scan(&root)
	if errors.Is(err, sql.ErrNoRows) {
		return ErrSyncForbidden
	}
	if err != nil {
		return err
	}
	if !grant.valid(root) {
		return ErrSyncForbidden
	}
	var revoked bool
	err = tx.QueryRowContext(ctx, `SELECT revoked_at IS NOT NULL FROM sync_sessions WHERE vault_id=$1 AND session_id=$2`, grant.VaultID, grant.SessionID).Scan(&revoked)
	if err == nil && revoked {
		return ErrSyncForbidden
	}
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	if err = insertSession(ctx, tx, grant, name, platform, osVersion); err != nil {
		return err
	}
	if err = notifySync(ctx, tx, userID, grant.VaultID, syncTopicSessions); err != nil {
		return err
	}
	return tx.Commit()
}

// Sessions lists the vault's signed-in sessions.
func (db *Store) Sessions(ctx context.Context, userID, vaultID string) ([]SyncSession, error) {
	rows, err := db.Conn.QueryContext(ctx, `SELECT s.session_id::text,s.name,s.platform,s.os_version,s.created_at,s.last_seen_at,s.revoked_at
		FROM sync_sessions s JOIN sync_vaults v USING(vault_id) WHERE v.user_id=$1 AND v.vault_id=$2 ORDER BY s.created_at`, userID, vaultID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []SyncSession{}
	for rows.Next() {
		var session SyncSession
		if err = rows.Scan(&session.SessionID, &session.Name, &session.Platform, &session.OSVersion, &session.CreatedAt, &session.LastSeenAt, &session.RevokedAt); err != nil {
			return nil, err
		}
		out = append(out, session)
	}
	return out, rows.Err()
}

// RenameSession names a session; any open session may name any other.
func (db *Store) RenameSession(ctx context.Context, userID, sessionID, name string) error {
	if !validSyncID(sessionID) || name == "" || len([]rune(name)) > 60 {
		return ErrSyncInvalid
	}
	tx, err := db.Conn.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	result, err := tx.ExecContext(ctx, `UPDATE sync_sessions s SET name=$3 FROM sync_vaults v WHERE v.vault_id=s.vault_id AND v.user_id=$1 AND s.session_id=$2 AND s.revoked_at IS NULL`, userID, sessionID, name)
	if err != nil {
		return err
	}
	if n, err := result.RowsAffected(); err != nil {
		return err
	} else if n != 1 {
		return ErrSyncForbidden
	}
	if err = notifySync(ctx, tx, userID, sessionID, syncTopicSessions); err != nil {
		return err
	}
	return tx.Commit()
}

// removalBytes is what a session holding the root signs to remove a session.
func removalBytes(vaultID, sessionID string) []byte {
	data, _ := json.Marshal([]any{"misty.sync.session-remove.v1", vaultID, sessionID})
	return data
}

// RemoveSession revokes a session for good. It needs the vault root's signature, so only a
// session that has the vault open can do it, not a stolen login.
func (db *Store) RemoveSession(ctx context.Context, userID, sessionID string, proof []byte) error {
	if !validSyncID(sessionID) || len(proof) != ed25519.SignatureSize {
		return ErrSyncInvalid
	}
	tx, err := db.Conn.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var vaultID string
	var root []byte
	err = tx.QueryRowContext(ctx, `SELECT vault_id::text,root_public_key FROM sync_vaults WHERE user_id=$1`, userID).Scan(&vaultID, &root)
	if errors.Is(err, sql.ErrNoRows) {
		return ErrSyncForbidden
	}
	if err != nil {
		return err
	}
	if len(root) != ed25519.PublicKeySize || !ed25519.Verify(root, removalBytes(vaultID, sessionID), proof) {
		return ErrSyncForbidden
	}
	if _, err = tx.ExecContext(ctx, `UPDATE sync_sessions SET revoked_at=COALESCE(revoked_at,now()) WHERE vault_id=$1 AND session_id=$2`, vaultID, sessionID); err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, `DELETE FROM sync_tickets WHERE vault_id=$1 AND session_id=$2`, vaultID, sessionID); err != nil {
		return err
	}
	if err = notifySync(ctx, tx, userID, sessionID, syncTopicSessions); err != nil {
		return err
	}
	return tx.Commit()
}

// RotateVaultKey replaces the sealed root after a password change. The transition must be signed
// by the root for the very next generation, so the server can't replay an older wrapper.
func (db *Store) RotateVaultKey(ctx context.Context, userID string, e SyncKeyEnvelope, t SyncKeyTransition) (*SyncVault, error) {
	if !e.Valid() {
		return nil, ErrSyncInvalid
	}
	tx, err := db.Conn.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	var vaultID string
	var root []byte
	var generation int64
	err = tx.QueryRowContext(ctx, `SELECT vault_id::text,root_public_key,key_generation FROM sync_vaults WHERE user_id=$1 FOR UPDATE`, userID).Scan(&vaultID, &root, &generation)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrSyncForbidden
	}
	if err != nil {
		return nil, err
	}
	if t.Generation != generation+1 || !t.valid(root, vaultID, e) {
		return nil, ErrSyncInvalid
	}
	wrapped, _ := json.Marshal(e)
	if _, err = tx.ExecContext(ctx, `UPDATE sync_vaults SET key_envelope=$1,key_generation=$2,key_transition_signature=$3 WHERE vault_id=$4`, string(wrapped), t.Generation, t.Signature, vaultID); err != nil {
		return nil, err
	}
	if err = notifySync(ctx, tx, userID, vaultID, syncTopicVaultKey); err != nil {
		return nil, err
	}
	if err = tx.Commit(); err != nil {
		return nil, err
	}
	return db.Vault(ctx, userID)
}

// destroyBytes is what a session holding the root signs to destroy the vault.
func destroyBytes(vaultID string) []byte {
	data, _ := json.Marshal([]any{"misty.sync.destroy.v1", vaultID})
	return data
}

// VerifyDestroyProof checks a root signature over the vault's destruction.
func (db *Store) VerifyDestroyProof(ctx context.Context, userID string, proof []byte) (bool, error) {
	v, err := db.Vault(ctx, userID)
	if v == nil || err != nil {
		return false, err
	}
	return len(proof) == ed25519.SignatureSize && len(v.RootPublicKey) == ed25519.PublicKeySize &&
		ed25519.Verify(v.RootPublicKey, destroyBytes(v.VaultID), proof), nil
}

// DeleteVault removes the account's vault and everything in it. The server only ever held its
// ciphertext. Reports whether there was one. Every open socket hears about it and stops.
func (db *Store) DeleteVault(ctx context.Context, userID string) (bool, error) {
	tx, err := db.Conn.BeginTx(ctx, nil)
	if err != nil {
		return false, err
	}
	defer tx.Rollback()
	result, err := tx.ExecContext(ctx, `DELETE FROM sync_vaults WHERE user_id=$1`, userID)
	if err != nil {
		return false, err
	}
	n, err := result.RowsAffected()
	if err != nil {
		return false, err
	}
	if n > 0 {
		if err = notifySync(ctx, tx, userID, "", syncTopicVaultGone); err != nil {
			return false, err
		}
	}
	return n > 0, tx.Commit()
}

// SyncConnectionIdentity is who a one-use ticket was minted for.
type SyncConnectionIdentity struct {
	UserID, VaultID, SessionID string
	PublicKey                  []byte
	// SessionHash names the account login that minted the ticket.
	SessionHash string
}

// CreateTicket mints a one-use socket ticket for an unrevoked session.
func (db *Store) CreateTicket(ctx context.Context, userID, vaultID, sessionID, hash, sessionHash string) error {
	if !validSyncID(vaultID) || !validSyncID(sessionID) || len(hash) != 64 {
		return ErrSyncInvalid
	}
	tx, err := db.Conn.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var allowed bool
	err = tx.QueryRowContext(ctx, `SELECT true FROM sync_sessions s JOIN sync_vaults v USING(vault_id) WHERE v.user_id=$1 AND v.vault_id=$2 AND s.session_id=$3 AND s.revoked_at IS NULL`, userID, vaultID, sessionID).Scan(&allowed)
	if errors.Is(err, sql.ErrNoRows) {
		return ErrSyncForbidden
	}
	if err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, `DELETE FROM sync_tickets WHERE vault_id=$1 AND session_id=$2 AND expires_at<=clock_timestamp()`, vaultID, sessionID); err != nil {
		return err
	}
	var pending int
	if err = tx.QueryRowContext(ctx, `SELECT count(*) FROM sync_tickets WHERE vault_id=$1 AND session_id=$2`, vaultID, sessionID).Scan(&pending); err != nil {
		return err
	}
	if pending >= 8 {
		return ErrSyncInvalid
	}
	if _, err = tx.ExecContext(ctx, `INSERT INTO sync_tickets(token_hash,vault_id,session_id,session_hash) VALUES($1,$2,$3,$4)`, hash, vaultID, sessionID, sessionHash); err != nil {
		return err
	}
	return tx.Commit()
}

// ConsumeTicket spends a ticket atomically.
func (db *Store) ConsumeTicket(ctx context.Context, hash string) (*SyncConnectionIdentity, error) {
	var i SyncConnectionIdentity
	err := db.Conn.QueryRowContext(ctx, `WITH consumed AS (DELETE FROM sync_tickets WHERE token_hash=$1 AND expires_at>clock_timestamp() RETURNING vault_id,session_id,session_hash)
		SELECT v.user_id,c.vault_id::text,c.session_id::text,s.public_key,c.session_hash FROM consumed c JOIN sync_vaults v USING(vault_id)
		JOIN sync_sessions s ON s.vault_id=c.vault_id AND s.session_id=c.session_id WHERE s.revoked_at IS NULL`, hash).Scan(&i.UserID, &i.VaultID, &i.SessionID, &i.PublicKey, &i.SessionHash)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrSyncForbidden
	}
	return &i, err
}

// NoteSeen records when a session was last connected. Called when a socket opens and closes, and
// not more often, so an idle connection costs the database nothing.
func (db *Store) NoteSeen(ctx context.Context, i SyncConnectionIdentity) error {
	_, err := db.Conn.ExecContext(ctx, `UPDATE sync_sessions SET last_seen_at=now() WHERE vault_id=$1 AND session_id=$2`, i.VaultID, i.SessionID)
	return err
}

// SessionActive reports whether a connected session may stay connected.
func (db *Store) SessionActive(ctx context.Context, i SyncConnectionIdentity) (bool, error) {
	var active bool
	err := db.Conn.QueryRowContext(ctx, `SELECT revoked_at IS NULL FROM sync_sessions WHERE vault_id=$1 AND session_id=$2`, i.VaultID, i.SessionID).Scan(&active)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	return active, err
}
