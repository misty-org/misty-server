package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func stringify(value any) string {
	data, _ := json.Marshal(value)
	return string(data)
}

func TestGenerateNeverOverwrites(t *testing.T) {
	store, _ := openStore(t.TempDir())
	store.Set("MISTY_AUTH_SIGNING_KEY", "kept")
	generated, err := generateMissing(store)
	if err != nil {
		t.Fatal(err)
	}
	if store.Get("MISTY_AUTH_SIGNING_KEY") != "kept" || len(generated) != len(secretSpecs)-1 {
		t.Fatalf("generated %v", generated)
	}
	if again, _ := generateMissing(store); len(again) != 0 {
		t.Fatalf("second run generated %v", again)
	}
	if !strings.HasPrefix(store.Get("MISTY_OPERATOR_TOKEN"), "misty_op_") || !strings.HasPrefix(store.Get("MISTY_BACKUP_AGE_RECIPIENT"), "age1") {
		t.Fatal("unexpected secret formats")
	}
}

func TestStoreKeepsPrivateValuesOutOfServerEnv(t *testing.T) {
	dir := t.TempDir()
	store, _ := openStore(dir)
	if _, err := generateMissing(store); err != nil {
		t.Fatal(err)
	}
	store.Set("CLOUDFLARE_TUNNEL_TOKEN", "tunnel-token")
	if err := store.Save(); err != nil {
		t.Fatal(err)
	}
	env, _ := os.ReadFile(filepath.Join(dir, "server.env"))
	for _, name := range []string{"DB_MIGRATION_PASSWORD", "CLOUDFLARE_TUNNEL_TOKEN", "MISTY_BACKUP_AGE_IDENTITY"} {
		if bytes.Contains(env, []byte(name+"=")) {
			t.Fatalf("%s leaked into server.env", name)
		}
	}
	if !bytes.Contains(env, []byte("DB_PASSWORD='")) {
		t.Fatal("server.env lacks the API's database password")
	}
	token, _ := os.ReadFile(filepath.Join(dir, "cloudflared", "token"))
	if strings.TrimSpace(string(token)) != "tunnel-token" {
		t.Fatal("tunnel token file missing")
	}
	reopened, err := openStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	if snapshot(reopened) != snapshot(store) {
		t.Fatal("a reopened store differs")
	}
	// The shell entrypoints source server.env.
	if _, err := exec.LookPath("sh"); err == nil {
		out, err := exec.Command("sh", "-c", `set -a; . "$1"; printf %s "$MISTY_AUTH_SIGNING_KEY"`, "sh", filepath.Join(dir, "server.env")).Output()
		if err != nil || string(out) != store.Get("MISTY_AUTH_SIGNING_KEY") {
			t.Fatalf("sh could not read server.env: %v", err)
		}
	}
}

func TestGuardRefusesLostSecrets(t *testing.T) {
	data := t.TempDir()
	if databaseHasData(data) {
		t.Fatal("an empty directory has no data")
	}
	if err := os.WriteFile(filepath.Join(data, "PG_VERSION"), []byte("16\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	empty, _ := openStore(t.TempDir())
	if err := guardData(empty, databaseHasData(data)); !errors.Is(err, errSecretsLost) {
		t.Fatalf("expected a refusal, got %v", err)
	}
	if err := guardData(empty, false); err != nil {
		t.Fatalf("a fresh stack must start: %v", err)
	}
	dir := t.TempDir()
	partial, _ := openStore(dir)
	generateMissing(partial)
	partial.Set("SPACE_LINK_ENCRYPTION_KEY", "")
	partial.Save()
	partial, _ = openStore(dir)
	if err := guardData(partial, true); !errors.Is(err, errSecretsLost) || !strings.Contains(err.Error(), "SPACE_LINK_ENCRYPTION_KEY") {
		t.Fatalf("expected a refusal naming the key, got %v", err)
	}
	partial.Set("SPACE_LINK_ENCRYPTION_KEY", "restored")
	partial.Set("MISTY_METRICS_TOKEN", "")
	if err := guardData(partial, true); err != nil {
		t.Fatalf("a missing secret that protects no data may be generated: %v", err)
	}
}

func TestRotation(t *testing.T) {
	store, _ := openStore(t.TempDir())
	generateMissing(store)
	old := store.Get("MISTY_AUTH_SIGNING_KEY")
	if err := rotate(store, "MISTY_AUTH_SIGNING_KEY"); err != nil {
		t.Fatal(err)
	}
	if store.Get("MISTY_AUTH_SIGNING_KEY_PREVIOUS") != old || store.Get("MISTY_AUTH_SIGNING_KEY") == old {
		t.Fatal("rotation did not keep the previous key")
	}
	device := store.Get("MISTY_DEVICE_TICKET_PRIVATE_KEY")
	public, _ := ed25519PublicKey(device)
	if err := rotate(store, "MISTY_DEVICE_TICKET_PRIVATE_KEY"); err != nil {
		t.Fatal(err)
	}
	if store.Get("MISTY_DEVICE_TICKET_PREVIOUS_PUBLIC_KEYS") != public {
		t.Fatal("the previous device key slot must hold the old public key")
	}
	for _, name := range []string{"SPACE_LINK_ENCRYPTION_KEY", "DB_MIGRATION_PASSWORD", "JOURNAL_COLLAB_ROOM_SALT", "UNKNOWN"} {
		if err := rotate(store, name); err == nil {
			t.Fatalf("%s must not rotate", name)
		}
	}
	if err := retire(store, "MISTY_AUTH_SIGNING_KEY"); err != nil || store.Has("MISTY_AUTH_SIGNING_KEY_PREVIOUS") {
		t.Fatal("retire did not clear the previous slot")
	}
}

func TestBackupKeyWorksWithAge(t *testing.T) {
	identity, err := newSecret(ageIdentity)
	if err != nil {
		t.Fatal(err)
	}
	recipient, err := ageRecipient(identity)
	if err != nil || !strings.HasPrefix(identity, "AGE-SECRET-KEY-1") {
		t.Fatal(err)
	}
	if _, err := exec.LookPath("age"); err != nil {
		t.Skip("age is not installed")
	}
	dir := t.TempDir()
	keyFile := filepath.Join(dir, "identity")
	os.WriteFile(keyFile, []byte(identity+"\n"), 0o600)
	encrypt := exec.Command("age", "--encrypt", "--recipient", recipient)
	encrypt.Stdin = strings.NewReader("backup")
	ciphertext, err := encrypt.Output()
	if err != nil {
		t.Fatalf("age rejected the recipient: %v", err)
	}
	decrypt := exec.Command("age", "--decrypt", "--identity", keyFile)
	decrypt.Stdin = bytes.NewReader(ciphertext)
	plain, err := decrypt.Output()
	if err != nil || string(plain) != "backup" {
		t.Fatalf("age rejected the identity: %v", err)
	}
}

func TestWranglerConfigsParse(t *testing.T) {
	bundle := writeBundles(t)
	collab, err := loadWorkerBundle(filepath.Join(bundle, "journal-collab"))
	if err != nil {
		t.Fatal(err)
	}
	if collab.Config.CompatibilityDate == "" || len(collab.Config.DurableObjects.Bindings) != 2 || len(collab.Config.Migrations) != 2 || collab.Config.Vars["JOURNAL_COLLAB_ISSUER"] != "misty-api" {
		t.Fatalf("collab config %+v", collab.Config)
	}
	if len(collab.Modules) != 1 {
		t.Fatalf("source maps must not upload: %v", collab.Modules)
	}
	var value map[string]any
	if err := decodeJSONC([]byte(`{"a": "// not a comment", /* c */ "b": [1, 2,], // x
	}`), &value); err != nil || value["a"] != "// not a comment" {
		t.Fatalf("jsonc: %v %v", value, err)
	}
}

func TestPendingMigrations(t *testing.T) {
	all := []map[string]any{{"tag": "v1", "new_sqlite_classes": []any{"NoteRoom"}}, {"tag": "v2", "new_sqlite_classes": []any{"DrawingRoom"}}}
	fresh, _ := pendingMigrations(all, "")
	if fresh["new_tag"] != "v2" || len(fresh["steps"].([]map[string]any)) != 2 || fresh["old_tag"] != nil {
		t.Fatalf("fresh %v", fresh)
	}
	upgrade, _ := pendingMigrations(all, "v1")
	if upgrade["old_tag"] != "v1" || len(upgrade["steps"].([]map[string]any)) != 1 {
		t.Fatalf("upgrade %v", upgrade)
	}
	if current, _ := pendingMigrations(all, "v2"); current != nil {
		t.Fatalf("current %v", current)
	}
	if _, err := pendingMigrations(all, "v9"); err == nil {
		t.Fatal("an unknown tag must fail")
	}
}

func TestInputsAreAllRequired(t *testing.T) {
	_, err := readInputs(func(string) string { return "" })
	if err == nil || !strings.Contains(err.Error(), "MISTY_COMPOSIO_API_KEY in server/.env/composio.env") || !strings.Contains(err.Error(), "GOOGLE_CLIENT_ID in server/.env/google.env") {
		t.Fatalf("expected every missing input, got %v", err)
	}
	names := resourceNames("a-very-long-hostname-that-goes-on.and-on.misty.example")
	if len(names.FetchWorker) > 63 || len(names.LibraryBucket) > 63 {
		t.Fatalf("names too long: %+v", names)
	}
}
