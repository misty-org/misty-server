package browsersync

import (
	"context"
	"crypto/ed25519"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"errors"
	"strconv"

)

// The legacy wrapper seals the root under a sync password plus a separate sync
// secret. The account wrapper seals it under a key the device derives from the
// person's Misty password alone; the server never sees that key.
const (
	SyncKDFLegacy  = "argon2id-m65536-t3-p1"
	SyncKDFAccount = "argon2id-m65536-t3-p1+hkdf-sha256.account-v2"
)

// SyncKeyTransition authorizes replacing a vault's password wrapper. The vault
// root signs it, so only a device holding the unlocked root can make one; the
// server stores it and checks it, and every client checks it again against the
// root public key it pinned. Generations only rise by one.
type SyncKeyTransition struct {
	Generation int64  `json:"generation"`
	Signature  []byte `json:"signature"`
}

// digest covers every wrapper field, so a transition authorizes exactly one
// wrapper. It must match `KeyEnvelope::digest` in the native sync crate.
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

// keyTransitionSigningBytes must match `KeyTransition::signing_bytes` in the
// native sync crate: a compact JSON array.
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

// RotateBrowserSyncVaultKey replaces the personal vault's password wrapper
// after a password change, or when a legacy vault is upgraded to the account
// wrapper. The new wrapper must be an account wrapper, and the transition must
// be signed by the vault's root for the very next generation, so the server
// cannot be used to replay an older wrapper or skip ahead.
func (db *Store) RotateBrowserSyncVaultKey(ctx context.Context, userID string, e SyncKeyEnvelope, t SyncKeyTransition) (*SyncVault, error) {
	if !e.Valid() || e.KDF != SyncKDFAccount {
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
	err = tx.QueryRowContext(ctx, `SELECT vault_id,root_public_key,key_generation FROM browser_sync_vaults WHERE user_id=$1 AND key_envelope IS NOT NULL FOR UPDATE`, userID).Scan(&vaultID, &root, &generation)
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
	if _, err = tx.ExecContext(ctx, `UPDATE browser_sync_vaults SET key_envelope=$1,key_generation=$2,key_transition_signature=$3 WHERE vault_id=$4`, string(wrapped), t.Generation, t.Signature, vaultID); err != nil {
		return nil, err
	}
	if err = tx.Commit(); err != nil {
		return nil, err
	}
	return db.BrowserSyncVault(ctx, userID)
}
