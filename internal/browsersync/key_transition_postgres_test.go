package browsersync

import (
	"context"
	"database/sql"
	"encoding/base64"
	"errors"
	"os"
	"strings"
	"testing"

	_ "github.com/lib/pq"
)

// Runs the real migration and store code against a throwaway PostgreSQL. Point
// MISTY_KEY_TRANSITION_TEST_DSN (key=value form) at an empty database whose name
// contains "test" or "check"; the test creates two small tables in it. The
// tables hold only the columns this code reads and writes: the full chain needs
// extensions (pgvector) that a plain Postgres lacks.
func TestRotateVaultKeyAgainstPostgres(t *testing.T) {
	dsn := os.Getenv("MISTY_KEY_TRANSITION_TEST_DSN")
	if dsn == "" {
		t.Skip("set MISTY_KEY_TRANSITION_TEST_DSN to an empty throwaway database to run this")
	}
	conn, err := sql.Open("postgres", dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	var name string
	if err = conn.QueryRow(`SELECT current_database()`).Scan(&name); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(name, "test") && !strings.Contains(name, "check") {
		t.Fatalf("refusing to create tables in %q: not a throwaway database", name)
	}
	ctx := context.Background()
	for _, statement := range []string{
		`DROP TABLE IF EXISTS public.browser_sync_vaults, public.account_cloud_mutations`,
		`CREATE TABLE public.browser_sync_vaults (
			user_id text NOT NULL, space_id text NOT NULL, vault_id uuid PRIMARY KEY,
			root_public_key bytea NOT NULL, key_epoch bigint NOT NULL DEFAULT 1,
			head_sequence bigint NOT NULL DEFAULT 0, key_envelope jsonb, space_key_envelope jsonb,
			created_at timestamptz NOT NULL DEFAULT now(),
			CONSTRAINT browser_sync_vaults_one_envelope CHECK ((key_envelope IS NULL) <> (space_key_envelope IS NULL)))`,
		`CREATE TABLE public.account_cloud_mutations (transaction_id bigint, user_id text, added_bytes bigint, removed_bytes bigint)`,
	} {
		if _, err = conn.Exec(statement); err != nil {
			t.Fatal(err)
		}
	}
	defer conn.Exec(`DROP TABLE IF EXISTS public.browser_sync_vaults, public.account_cloud_mutations`)
	// The migration under test, exactly as goose would run its Up section.
	migration, err := os.ReadFile("../platform/db/migrations/20271105000000_sync_vault_key_transition.sql")
	if err != nil {
		t.Fatal(err)
	}
	up := strings.SplitN(strings.SplitN(string(migration), "-- +goose Up", 2)[1], "-- +goose Down", 2)[0]
	if _, err = conn.Exec(up); err != nil {
		t.Fatalf("migration failed: %v", err)
	}

	root, envelope, transition := vectorParts(t)
	legacy := `{"version":1,"kdf":"` + SyncKDFLegacy + `","salt":"` + vectorSalt + `","nonce":"` + vectorNonce + `","ciphertext":"` + vectorCiphertext + `"}`
	if _, err = conn.Exec(`INSERT INTO browser_sync_vaults(user_id,space_id,vault_id,root_public_key,key_envelope) VALUES('owner','space',$1,$2,$3)`, vectorVault, root, legacy); err != nil {
		t.Fatal(err)
	}
	store := NewStore(conn)

	// A fresh vault has no transition.
	vault, err := store.BrowserSyncVault(ctx, "owner")
	if err != nil || vault == nil || vault.KeyTransition != nil {
		t.Fatalf("a vault that was never rotated reports a transition: %v %+v", err, vault)
	}
	// Refused: not an account wrapper, another account, a signature for another generation.
	if _, err = store.RotateBrowserSyncVaultKey(ctx, "owner", SyncKeyEnvelope{Version: 1, KDF: SyncKDFLegacy, Salt: vectorSalt, Nonce: vectorNonce, Ciphertext: vectorCiphertext}, transition); !errors.Is(err, ErrSyncInvalid) {
		t.Fatalf("a legacy wrapper was accepted: %v", err)
	}
	if _, err = store.RotateBrowserSyncVaultKey(ctx, "someone else", envelope, transition); !errors.Is(err, ErrSyncForbidden) {
		t.Fatalf("another account rotated this vault: %v", err)
	}
	skipped := transition
	skipped.Generation = 2
	if _, err = store.RotateBrowserSyncVaultKey(ctx, "owner", envelope, skipped); !errors.Is(err, ErrSyncInvalid) {
		t.Fatalf("skipping a generation was accepted: %v", err)
	}
	forged := transition
	forged.Signature = append([]byte(nil), transition.Signature...)
	forged.Signature[0] ^= 1
	if _, err = store.RotateBrowserSyncVaultKey(ctx, "owner", envelope, forged); !errors.Is(err, ErrSyncInvalid) {
		t.Fatalf("a forged signature was accepted: %v", err)
	}
	// Accepted: the signature the native crate made.
	rotated, err := store.RotateBrowserSyncVaultKey(ctx, "owner", envelope, transition)
	if err != nil {
		t.Fatalf("the native signature was refused: %v", err)
	}
	if rotated.KeyTransition == nil || rotated.KeyTransition.Generation != 1 || base64.StdEncoding.EncodeToString(rotated.KeyTransition.Signature) != vectorSignature {
		t.Fatalf("the stored transition is wrong: %+v", rotated.KeyTransition)
	}
	if !strings.Contains(string(rotated.KeyEnvelope), SyncKDFAccount) {
		t.Fatalf("the stored wrapper is not the account wrapper: %s", rotated.KeyEnvelope)
	}
	// Replaying the same transition is refused: generations only rise by one.
	if _, err = store.RotateBrowserSyncVaultKey(ctx, "owner", envelope, transition); !errors.Is(err, ErrSyncInvalid) {
		t.Fatalf("a replay was accepted: %v", err)
	}
	// The database refuses a rotated vault with no signature, and a signature on an unrotated one.
	if _, err = conn.Exec(`UPDATE browser_sync_vaults SET key_transition_signature=NULL WHERE vault_id=$1`, vectorVault); err == nil {
		t.Fatal("the check constraint allowed a generation with no signature")
	}
	if _, err = conn.Exec(`UPDATE browser_sync_vaults SET key_generation=0 WHERE vault_id=$1`, vectorVault); err == nil {
		t.Fatal("the check constraint allowed a signature on generation 0")
	}
}
