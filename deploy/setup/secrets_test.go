package main

import (
	"bytes"
	"crypto/rand"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/joho/godotenv"

	"github.com/misty-org/misty-server/internal/platform/serverenv"
)

func stringify(value any) string {
	data, _ := json.Marshal(value)
	return string(data)
}

func freshStore(t *testing.T) (*Store, string) {
	t.Helper()
	dir := t.TempDir()
	store, err := openStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	if made, err := ensureRoot(store); err != nil || !made {
		t.Fatalf("root not made: %v", err)
	}
	if err := store.Save(); err != nil {
		t.Fatal(err)
	}
	return store, dir
}

func legacyStore(t *testing.T) (*Store, string, map[string]string) {
	t.Helper()
	dir := t.TempDir()
	store, _ := openStore(dir)
	values := map[string]string{}
	for _, spec := range secretSpecs {
		raw := make([]byte, 32)
		rand.Read(raw)
		value, err := serverenv.Encode(spec.Format, raw)
		if err != nil {
			t.Fatal(err)
		}
		values[spec.Name] = value
		store.Set(spec.Name, value)
	}
	values["DOCUMENT_SIGNING_KEY"] = "legacy-document-key"
	store.Set("DOCUMENT_SIGNING_KEY", values["DOCUMENT_SIGNING_KEY"])
	store.Set("JOURNAL_COLLAB_ROOM_SALT", "legacy-room-salt")
	store.Set("JOURNAL_COLLAB_TICKET_PUBLIC_KEY", "legacy-ticket-public-key")
	store.Set("PARTYKIT_HOST", "misty-cloud-misty-example-collab.misty.workers.dev")
	store.State["worker:misty-cloud-misty-example-collab"] = "fingerprint"
	store.Set("MISTY_PUBLIC_API_URL", "https://cloud.misty.example/v1")
	store.Set("R2_BUCKET", "misty-space-drive")
	store.Set("MISTY_BACKUP_BUCKET", "misty-db-backups")
	store.Set("MISTY_CLOUDFLARE_TUNNEL_ID", "tunnel-1")
	if err := store.Save(); err != nil {
		t.Fatal(err)
	}
	return store, dir, values
}

func TestAFreshStackStoresOnlyTheRoot(t *testing.T) {
	store, dir := freshStore(t)
	if names := sortedKeys(store.Values); len(names) != 1 || names[0] != serverenv.RootName {
		t.Fatalf("stored %v", names)
	}
	for _, spec := range secretSpecs {
		if !store.Has(spec.Name) {
			t.Fatalf("%s is not derived", spec.Name)
		}
	}
	if !strings.HasPrefix(store.Get("MISTY_OPERATOR_TOKEN"), "misty_op_") || !strings.HasPrefix(store.Get("MISTY_BACKUP_AGE_RECIPIENT"), "age1") ||
		!strings.HasPrefix(store.Get("MISTY_BACKUP_AGE_IDENTITY"), "AGE-SECRET-KEY-1") {
		t.Fatal("unexpected secret formats")
	}
	if made, _ := ensureRoot(store); made {
		t.Fatal("a second run made another root")
	}
	reopened, _ := openStore(dir)
	if snapshot(reopened) != snapshot(store) {
		t.Fatalf("a reopened store differs:\n%s\n%s", snapshot(reopened), snapshot(store))
	}
	for _, spec := range secretSpecs {
		if reopened.Get(spec.Name) != store.Get(spec.Name) {
			t.Fatalf("%s changed on reopening", spec.Name)
		}
	}
}

func TestServerEnvGivesTheAPIItsKeysAndNothingMore(t *testing.T) {
	store, dir := freshStore(t)
	store.Set("CLOUDFLARE_TUNNEL_TOKEN", "tunnel-token")
	if err := store.Save(); err != nil {
		t.Fatal(err)
	}
	env, _ := os.ReadFile(filepath.Join(dir, "server.env"))
	for _, name := range []string{serverenv.RootName, "DB_MIGRATION_PASSWORD", "CLOUDFLARE_TUNNEL_TOKEN", "MISTY_BACKUP_AGE_IDENTITY", "MISTY_AUTH_SIGNING_KEY"} {
		if bytes.Contains(env, []byte(name+"=")) {
			t.Fatalf("%s is in server.env:\n%s", name, env)
		}
	}
	for name, file := range map[string]string{
		"DB_MIGRATION_PASSWORD": "postgres/superuser-password", "MISTY_BACKUP_AGE_IDENTITY": "backup/identity",
		"CLOUDFLARE_TUNNEL_TOKEN": "cloudflared/token", serverenv.RootName: "server-secret",
		"MISTY_OPERATOR_TOKEN": "operator/token",
	} {
		data, _ := os.ReadFile(filepath.Join(dir, file))
		if strings.TrimSpace(string(data)) != store.Get(name) {
			t.Fatalf("%s is not in %s", name, file)
		}
	}
	values, err := godotenv.Read(filepath.Join(dir, "server.env"))
	if err != nil {
		t.Fatal(err)
	}
	values["MISTY_ENVIRONMENT"], values["MISTY_HOSTNAME"] = "production", "cloud.misty.example"
	defaults, err := serverenv.Defaults(func(name string) string { return values[name] })
	if err != nil {
		t.Fatal(err)
	}
	for _, spec := range secretSpecs {
		if spec.Scope == serverenv.FromRoot {
			if defaults[spec.Name] != "" {
				t.Fatalf("the API can derive %s", spec.Name)
			}
			continue
		}
		if defaults[spec.Name] != store.Get(spec.Name) {
			t.Fatalf("the API and setup disagree on %s", spec.Name)
		}
	}
	if defaults["MISTY_PUBLIC_API_URL"] != "https://cloud.misty.example/v1" || defaults["R2_BUCKET"] != "misty-space-drive" {
		t.Fatalf("hostname values %v", defaults)
	}
	if _, err := exec.LookPath("sh"); err == nil {
		out, err := exec.Command("sh", "-c", `set -a; . "$1"; printf %s "$MISTY_API_SECRET"`, "sh", filepath.Join(dir, "server.env")).Output()
		if err != nil || string(out) != store.Get(serverenv.APISecretName) {
			t.Fatalf("sh could not read server.env: %v", err)
		}
	}
}

func TestAVolumeFromBeforeTheRootKeepsItsKeys(t *testing.T) {
	_, dir, legacy := legacyStore(t)
	store, err := openStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	if err := guardData(store, true); err != nil {
		t.Fatalf("a volume from before the root must start over its data: %v", err)
	}
	if made, err := ensureRoot(store); err != nil || !made {
		t.Fatalf("root: %v", err)
	}
	if err := store.Save(); err != nil {
		t.Fatal(err)
	}
	store, _ = openStore(dir)
	for name, value := range legacy {
		if store.Get(name) != value {
			t.Fatalf("%s changed", name)
		}
	}
	if store.State[tunnelState] != "tunnel-1" {
		t.Fatal("the tunnel ID did not move into state.json")
	}
	env, _ := os.ReadFile(filepath.Join(dir, "server.env"))
	for _, name := range []string{"MISTY_PUBLIC_API_URL", "R2_BUCKET", "MISTY_BACKUP_BUCKET", "MISTY_CLOUDFLARE_TUNNEL_ID", "JOURNAL_COLLAB_TICKET_PUBLIC_KEY", "PARTYKIT_HOST"} {
		if bytes.Contains(env, []byte(name+"=")) {
			t.Fatalf("server.env still stores %s", name)
		}
	}
	if store.Get("JOURNAL_COLLAB_ROOM_SALT") != "legacy-room-salt" {
		t.Fatal("a retired key went before adopt-root")
	}
	if _, ok := store.State["worker:misty-cloud-misty-example-collab"]; ok {
		t.Fatal("state.json still records a Worker upload")
	}
	values, _ := godotenv.Read(filepath.Join(dir, "server.env"))
	defaults, _ := serverenv.Defaults(func(name string) string { return values[name] })
	for _, spec := range secretSpecs {
		if spec.Scope == serverenv.FromAPISecret && (values[spec.Name] != legacy[spec.Name] || defaults[spec.Name] != "") {
			t.Fatalf("the API would not use the stored %s", spec.Name)
		}
	}
	store.Set("SPACE_LINK_ENCRYPTION_KEY", "")
	if err := guardData(store, true); !errors.Is(err, errSecretsLost) || !strings.Contains(err.Error(), "SPACE_LINK_ENCRYPTION_KEY") {
		t.Fatalf("expected a refusal naming the key, got %v", err)
	}
}

func TestAdoptRootMovesKeysAndKeepsSessions(t *testing.T) {
	_, dir, legacy := legacyStore(t)
	store, _ := openStore(dir)
	ensureRoot(store)
	moved, kept, err := adoptRoot(store, false)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(kept, " ") != "SPACE_LINK_ENCRYPTION_KEY MISTY_BACKUP_AGE_IDENTITY" || len(moved) != len(secretSpecs)-2+2 {
		t.Fatalf("moved %v, kept %v", moved, kept)
	}
	for _, name := range kept {
		if store.Get(name) != legacy[name] {
			t.Fatalf("%s changed without --discard-data", name)
		}
	}
	if store.Get("MISTY_AUTH_SIGNING_KEY") == legacy["MISTY_AUTH_SIGNING_KEY"] || store.Get("MISTY_AUTH_SIGNING_KEY_PREVIOUS") != legacy["MISTY_AUTH_SIGNING_KEY"] {
		t.Fatal("the old signing key must stay valid")
	}
	if store.Get("DB_MIGRATION_PASSWORD_PREVIOUS") != legacy["DB_MIGRATION_PASSWORD"] || store.Stored("DB_PASSWORD") || store.Stored("DOCUMENT_SIGNING_KEY") || store.Stored("JOURNAL_COLLAB_ROOM_SALT") {
		t.Fatal("database passwords did not move")
	}
	if err := store.Save(); err != nil {
		t.Fatal(err)
	}
	env, _ := os.ReadFile(filepath.Join(dir, "server.env"))
	if bytes.Contains(env, []byte("DB_MIGRATION_PASSWORD")) {
		t.Fatal("the old superuser password leaked into server.env")
	}
	for _, spec := range secretSpecs {
		if spec.Previous != "" {
			retire(store, spec.Name)
		}
	}
	store.Save()
	store, _ = openStore(dir)
	if _, err := os.Stat(filepath.Join(dir, "postgres/superuser-password-previous")); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("retire left the old superuser password on disk")
	}
	if strings.Join(sortedKeys(store.Values), " ") != "MISTY_BACKUP_AGE_IDENTITY MISTY_SERVER_SECRET SPACE_LINK_ENCRYPTION_KEY" {
		t.Fatalf("stored after adopt-root and retire: %v", sortedKeys(store.Values))
	}
	if _, _, err := adoptRoot(store, true); err != nil || len(storedKeys(store)) != 0 {
		t.Fatalf("--discard-data left %v (%v)", storedKeys(store), err)
	}
	if err := guardData(store, true); err != nil {
		t.Fatalf("a stack on the root must start over its data: %v", err)
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
	store, dir := freshStore(t)
	if err := guardData(store, true); err != nil {
		t.Fatalf("a stack on the root must start: %v", err)
	}
	if err := os.Remove(filepath.Join(dir, "server-secret")); err != nil {
		t.Fatal(err)
	}
	lost, _ := openStore(dir)
	if err := guardData(lost, true); !errors.Is(err, errSecretsLost) || !strings.Contains(err.Error(), serverenv.RootName) {
		t.Fatalf("expected a refusal naming the root, got %v", err)
	}
}

func TestRotation(t *testing.T) {
	store, _ := freshStore(t)
	old := store.Get("MISTY_AUTH_SIGNING_KEY")
	if err := rotate(store, "MISTY_AUTH_SIGNING_KEY"); err != nil {
		t.Fatal(err)
	}
	if store.Get("MISTY_AUTH_SIGNING_KEY_PREVIOUS") != old || store.Get("MISTY_AUTH_SIGNING_KEY") == old || store.Stored("MISTY_AUTH_SIGNING_KEY") {
		t.Fatal("rotation did not keep the previous key")
	}
	if store.State["key:auth-signing:version"] != "2" || store.Get(serverenv.VersionsName) != "auth-signing=2/1" {
		t.Fatalf("versions %v", store.State)
	}
	metrics := store.Get("MISTY_METRICS_TOKEN")
	if err := rotate(store, "MISTY_METRICS_TOKEN"); err != nil || store.Get("MISTY_METRICS_TOKEN") == metrics {
		t.Fatalf("a key without a previous slot rotates at restart: %v", err)
	}
	for _, name := range []string{"SPACE_LINK_ENCRYPTION_KEY", "MISTY_BACKUP_AGE_IDENTITY", "JOURNAL_COLLAB_TICKET_PRIVATE_KEY", "UNKNOWN"} {
		if err := rotate(store, name); err == nil {
			t.Fatalf("%s must not rotate", name)
		}
	}
	if err := retire(store, "MISTY_AUTH_SIGNING_KEY"); err != nil || store.Has("MISTY_AUTH_SIGNING_KEY_PREVIOUS") {
		t.Fatal("retire did not clear the previous slot")
	}
	if names := sortedKeys(store.Values); len(names) != 1 {
		t.Fatalf("stored %v", names)
	}
}

func TestBackupKeyWorksWithAge(t *testing.T) {
	store, _ := freshStore(t)
	identity := store.Get("MISTY_BACKUP_AGE_IDENTITY")
	recipient, err := serverenv.AgeRecipient(identity)
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

func TestInputsAreAllRequired(t *testing.T) {
	_, err := readInputs(func(string) string { return "" })
	if err == nil || !strings.Contains(err.Error(), "CLOUDFLARE_API_TOKEN in ~/.misty/.env/server/cloudflare.env") || !strings.Contains(err.Error(), "GOOGLE_CLIENT_ID in ~/.misty/.env/server/google.env") {
		t.Fatalf("expected every missing input, got %v", err)
	}
	if strings.Contains(err.Error(), "CLOUDFLARE_ACCOUNT_ID") || strings.Contains(err.Error(), "CLOUDFLARE_ZONE_ID") {
		t.Fatalf("the account and zone come from Cloudflare: %v", err)
	}
	names := resourceNames("a-very-long-hostname-that-goes-on.and-on.misty.example")
	if len(names.LibraryBucket) > 63 || len(names.BackupBucket) > 63 {
		t.Fatalf("names too long: %+v", names)
	}
}
