package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/misty-org/misty-server/deploy/setup/fakecloudflare"
	"github.com/misty-org/misty-server/internal/platform/serverenv"
)

const (
	testAccount = "0123456789abcdef0123456789abcdef"
	testZone    = "fedcba9876543210fedcba9876543210"
)

type harness struct {
	fake    *fakecloudflare.Fake
	inputs  Inputs
	store   *Store
	bundles string
}

func newHarness(t *testing.T) *harness {
	t.Helper()
	return newHarnessWith(t, nil)
}

// newHarnessWith adds or replaces inputs on top of the defaults.
func newHarnessWith(t *testing.T, extra map[string]string) *harness {
	t.Helper()
	fake := fakecloudflare.New("cf-token", testAccount, testZone, "misty.example")
	server := httptest.NewServer(fake)
	t.Cleanup(server.Close)
	// cloudflare.env holds only the API token: setup finds the zone and
	// the account.
	values := map[string]string{
		"MISTY_HOSTNAME": "cloud.misty.example", "CLOUDFLARE_API_TOKEN": "cf-token",
		"GOOGLE_CLIENT_ID": "g", "GOOGLE_CLIENT_SECRET": "g",
		"MISTY_CLOUDFLARE_API_URL": server.URL + "/client/v4",
	}
	for name, value := range extra {
		values[name] = value
	}
	inputs, err := readInputs(func(name string) string { return values[name] })
	if err != nil {
		t.Fatal(err)
	}
	store, err := openStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := ensureRoot(store); err != nil {
		t.Fatal(err)
	}
	return &harness{fake: fake, inputs: inputs, store: store, bundles: writeBundles(t)}
}

// writeBundles copies the real wrangler.jsonc files next to a stand-in bundle.
func writeBundles(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	for _, app := range []string{"journal-collab"} {
		config, err := os.ReadFile(filepath.Join("..", "..", "services", app, "wrangler.jsonc"))
		if err != nil {
			t.Fatal(err)
		}
		dir := filepath.Join(root, app)
		if err := os.MkdirAll(filepath.Join(dir, "dist"), 0o755); err != nil {
			t.Fatal(err)
		}
		for name, data := range map[string]string{"wrangler.jsonc": string(config), "dist/index.js": "export default {fetch(){}}", "dist/index.js.map": "{}"} {
			if err := os.WriteFile(filepath.Join(dir, name), []byte(data), 0o644); err != nil {
				t.Fatal(err)
			}
		}
	}
	return root
}

func (h *harness) provision(t *testing.T) error {
	t.Helper()
	cf := newCloudflare(h.inputs)
	names := resourceNames(h.inputs.Hostname)
	ctx := context.Background()
	inputs := h.inputs
	zone, err := resolveZone(ctx, cf, &inputs)
	if err != nil {
		return err
	}
	if err := provisionTunnel(ctx, cf, inputs, zone, names, h.store); err != nil {
		return err
	}
	if err := provisionR2(ctx, cf, inputs, names, h.store); err != nil {
		return err
	}
	return provisionWorkers(ctx, cf, inputs, names, h.store, h.bundles)
}

func TestProvisionCreatesEverythingOnce(t *testing.T) {
	h := newHarness(t)
	if err := h.provision(t); err != nil {
		t.Fatal(err)
	}
	fake := h.fake
	if len(fake.Tunnels) != 1 || len(fake.DNS) != 1 || len(fake.Tokens) != 2 || len(fake.Buckets) != 2 || len(fake.Scripts) != 1 {
		t.Fatalf("resources: tunnels=%d dns=%d tokens=%d buckets=%d scripts=%d", len(fake.Tunnels), len(fake.DNS), len(fake.Tokens), len(fake.Buckets), len(fake.Scripts))
	}
	for _, record := range fake.DNS {
		if record["type"] != "CNAME" || record["proxied"] != true || !strings.HasSuffix(record["content"].(string), ".cfargotunnel.com") {
			t.Fatalf("dns record %v", record)
		}
	}
	for _, config := range fake.Configs {
		ingress := config.(map[string]any)["ingress"].([]any)
		if first := ingress[0].(map[string]any); first["hostname"] != "cloud.misty.example" || first["service"] != apiService {
			t.Fatalf("ingress %v", ingress)
		}
	}
	names := resourceNames("cloud.misty.example")
	if fake.Buckets[names.LibraryBucket].CORS == nil || fake.Buckets[names.BackupBucket].Lock == nil || fake.Buckets[names.BackupBucket].Lifecycle == nil {
		t.Fatal("bucket rules were not set")
	}
	for _, token := range fake.Tokens {
		if !strings.Contains(stringify(token.Policies), "_default_misty-cloud-misty-example-") {
			t.Fatalf("token %s is not scoped to one bucket: %v", token.Name, token.Policies)
		}
	}
	library := fake.Tokens[h.store.Get("R2_ACCESS_KEY")]
	sum := sha256.Sum256([]byte(library.Value))
	if library.Name != names.LibraryToken || h.store.Get("R2_SECRET_KEY") != hex.EncodeToString(sum[:]) {
		t.Fatal("library credentials do not match the token")
	}
	collab := fake.Scripts[names.CollabWorker]
	// The Worker's newest migration tag (v7 added AppRoom).
	if collab.MigrationTag != "v7" || !collab.Subdomain || !strings.Contains(stringify(collab.Metadata["bindings"]), "JOURNAL_COLLAB_TICKET_PUBLIC_KEY") {
		t.Fatalf("collab worker %+v", collab.Metadata)
	}
	for _, name := range []string{"JOURNAL_COLLAB_TICKET_PRIVATE_KEY", serverenv.RootName, serverenv.APISecretName, "JOURNAL_COLLAB_ROOM_SALT", "MISTY_AUTH_SIGNING_KEY"} {
		if strings.Contains(stringify(collab.Metadata), h.store.Get(name)) {
			t.Fatalf("%s was sent to Cloudflare", name)
		}
	}
	for _, name := range []string{"JOURNAL_COLLAB_CONTROL_SECRET", "JOURNAL_COLLAB_PROJECTION_SECRET", "JOURNAL_COLLAB_TICKET_PUBLIC_KEY"} {
		if !strings.Contains(stringify(collab.Metadata["bindings"]), h.store.Get(name)) {
			t.Fatalf("the Worker did not get the derived %s", name)
		}
	}
	if h.store.Get("R2_ENDPOINT") != "https://"+testAccount+".r2.cloudflarestorage.com" {
		t.Fatalf("R2 endpoint %s: the account comes from the zone", h.store.Get("R2_ENDPOINT"))
	}
	if !strings.Contains(stringify(collab.Metadata["bindings"]), "https://cloud.misty.example/v1") {
		t.Fatal("collab worker does not call back to the API")
	}
	if h.store.Get("PARTYKIT_HOST") != names.CollabWorker+".misty-test.workers.dev" {
		t.Fatalf("worker host %s", h.store.Get("PARTYKIT_HOST"))
	}
	if !strings.HasPrefix(h.store.Get("CLOUDFLARE_TUNNEL_TOKEN"), "fake-tunnel-token-") {
		t.Fatal("tunnel token was not saved")
	}

	before := snapshot(h.store)
	requests := len(fake.Requests)
	if err := h.provision(t); err != nil {
		t.Fatal(err)
	}
	if len(fake.Tunnels) != 1 || len(fake.DNS) != 1 || len(fake.Tokens) != 2 || fake.Scripts[names.CollabWorker].Uploads != 1 {
		t.Fatal("a second run created or uploaded again")
	}
	if fake.Count("POST /accounts/"+testAccount+"/tokens") != 2 || len(fake.Requests) == requests {
		t.Fatal("unexpected token requests")
	}
	if after := snapshot(h.store); after != before {
		t.Fatal("a second run changed stored values")
	}
}

func TestLostTokenSecretRollsTheSameToken(t *testing.T) {
	h := newHarness(t)
	if err := h.provision(t); err != nil {
		t.Fatal(err)
	}
	id, secret := h.store.Get("R2_ACCESS_KEY"), h.store.Get("R2_SECRET_KEY")
	h.store.Set("R2_SECRET_KEY", "")
	if err := h.provision(t); err != nil {
		t.Fatal(err)
	}
	if len(h.fake.Tokens) != 2 || h.store.Get("R2_ACCESS_KEY") != id || h.store.Get("R2_SECRET_KEY") == secret || !h.store.Has("R2_SECRET_KEY") {
		t.Fatal("expected the existing token's value to be rolled")
	}
}

func TestRotationRedeploysTheWorker(t *testing.T) {
	h := newHarness(t)
	if err := h.provision(t); err != nil {
		t.Fatal(err)
	}
	old := h.store.Get("JOURNAL_COLLAB_TICKET_PUBLIC_KEY")
	if err := rotate(h.store, "JOURNAL_COLLAB_TICKET_PRIVATE_KEY"); err != nil {
		t.Fatal(err)
	}
	if err := h.provision(t); err != nil {
		t.Fatal(err)
	}
	collab := h.fake.Scripts[resourceNames("cloud.misty.example").CollabWorker]
	if collab.Uploads != 2 || !strings.Contains(stringify(collab.Metadata["bindings"]), "JOURNAL_COLLAB_TICKET_PUBLIC_KEY_PREVIOUS") {
		t.Fatal("rotation did not redeploy with the previous key")
	}
	if h.store.Get("JOURNAL_COLLAB_TICKET_PUBLIC_KEY_PREVIOUS") != old {
		t.Fatal("previous public key not kept")
	}
	if _, ok := collab.Metadata["migrations"]; ok {
		t.Fatal("a current script got migrations again")
	}
}

func TestForeignDNSRecordStopsSetup(t *testing.T) {
	h := newHarness(t)
	h.fake.DNS["existing"] = map[string]any{"id": "existing", "type": "A", "name": "cloud.misty.example", "content": "203.0.113.1"}
	err := h.provision(t)
	if err == nil || !strings.Contains(err.Error(), "already has a A record") {
		t.Fatalf("expected a DNS conflict, got %v", err)
	}
	if h.fake.Count("POST /zones/") != 0 {
		t.Fatal("setup changed DNS despite the conflict")
	}
}

func TestWrongZoneStopsSetup(t *testing.T) {
	h := newHarness(t)
	h.fake.ZoneName = "other.example"
	if err := h.provision(t); err == nil || !strings.Contains(err.Error(), "no zone that holds") {
		t.Fatalf("expected a zone error, got %v", err)
	}
	pinned := newHarnessWith(t, map[string]string{"CLOUDFLARE_ZONE_ID": testZone})
	pinned.fake.ZoneName = "other.example"
	if err := pinned.provision(t); err == nil || !strings.Contains(err.Error(), "not in the zone") {
		t.Fatalf("expected a zone error, got %v", err)
	}
}

func TestZoneAndAccountSetByTheOperatorMustMatch(t *testing.T) {
	h := newHarnessWith(t, map[string]string{"CLOUDFLARE_ACCOUNT_ID": testAccount, "CLOUDFLARE_ZONE_ID": testZone})
	if err := h.provision(t); err != nil {
		t.Fatalf("the old cloudflare.env keys still work: %v", err)
	}
	other := newHarnessWith(t, map[string]string{"CLOUDFLARE_ACCOUNT_ID": "11111111111111111111111111111111", "CLOUDFLARE_ZONE_ID": testZone})
	if err := other.provision(t); err == nil || !strings.Contains(err.Error(), "another account") {
		t.Fatalf("expected an account mismatch, got %v", err)
	}
	filtered := newHarnessWith(t, map[string]string{"CLOUDFLARE_ACCOUNT_ID": "11111111111111111111111111111111"})
	if err := filtered.provision(t); err == nil || !strings.Contains(err.Error(), "no zone that holds") {
		t.Fatalf("an account without the zone must fail, got %v", err)
	}
}

func TestOutageAfterASuccessfulRunKeepsTheStackStarting(t *testing.T) {
	h := newHarness(t)
	t.Setenv("MISTY_WORKERS_DIR", h.bundles)
	if err := provision(context.Background(), h.inputs, h.store); err != nil {
		t.Fatal(err)
	}
	h.fake.Fail = true
	if err := provision(context.Background(), h.inputs, h.store); err != nil {
		t.Fatalf("an outage after a successful run should not stop the stack: %v", err)
	}
	fresh := newHarness(t)
	fresh.fake.Fail = true
	if err := provision(context.Background(), fresh.inputs, fresh.store); err == nil {
		t.Fatal("a first run must fail while Cloudflare is down")
	}
}

func TestDroppingTheZoneFromCloudflareEnvKeepsTheOutageFallback(t *testing.T) {
	h := newHarnessWith(t, map[string]string{"CLOUDFLARE_ACCOUNT_ID": testAccount, "CLOUDFLARE_ZONE_ID": testZone})
	t.Setenv("MISTY_WORKERS_DIR", h.bundles)
	if err := provision(context.Background(), h.inputs, h.store); err != nil {
		t.Fatal(err)
	}
	h.fake.Fail = true
	tokenOnly := h.inputs
	tokenOnly.AccountID, tokenOnly.ZoneID = "", ""
	if err := provision(context.Background(), tokenOnly, h.store); err != nil {
		t.Fatalf("the recorded zone and account stand in for the removed lines: %v", err)
	}
}

func TestErrorsNeverCarryTheToken(t *testing.T) {
	h := newHarness(t)
	h.fake.APIToken = "another-token"
	err := h.provision(t)
	if err == nil || strings.Contains(err.Error(), "cf-token") || !strings.Contains(err.Error(), "403") {
		t.Fatalf("unexpected error %v", err)
	}
}

func TestMissingWorkersDevSubdomain(t *testing.T) {
	h := newHarness(t)
	h.fake.Subdomain = ""
	if err := h.provision(t); err == nil || !strings.Contains(err.Error(), "workers.dev") {
		t.Fatalf("expected a workers.dev error, got %v", err)
	}
}

func snapshot(store *Store) string {
	var b strings.Builder
	for _, name := range sortedKeys(store.Values) {
		b.WriteString(name + "=" + store.Values[name] + "\n")
	}
	return b.String()
}
