package api

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"strings"
	"sync"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/misty-org/misty-server/internal/browsersync"
	"github.com/misty-org/misty-server/internal/platform/db"
)

type syncTestVault struct {
	database *db.Database
	store    *browsersync.Store
	router   *chi.Mux
	user     string
	token    string
	vaultID  string
	root     ed25519.PrivateKey
	session  browsersync.SyncConnectionIdentity
}

func syncTestEnvelope() browsersync.SyncKeyEnvelope {
	encoded := func(n int) string { return base64.StdEncoding.EncodeToString(make([]byte, n)) }
	return browsersync.SyncKeyEnvelope{Version: 1, KDF: "argon2id-m131072-t3-p1.vault-v1", Salt: encoded(32), Nonce: encoded(12), Ciphertext: encoded(48)}
}

func syncTestGrant(vaultID, sessionID string, public ed25519.PublicKey, root ed25519.PrivateKey) browsersync.SyncSessionGrant {
	grant := browsersync.SyncSessionGrant{VaultID: vaultID, SessionID: sessionID, PublicKey: public}
	grant.Signature = ed25519.Sign(root, grant.SigningBytes())
	return grant
}

func syncTestUser(t *testing.T, database *db.Database) (string, string) {
	t.Helper()
	user, err := database.CreateUserWithUsername("sv"+strings.ReplaceAll(uuid.NewString()[:13], "-", ""), uniqueTestEmail("sync"), "password123")
	if err != nil {
		t.Fatal(err)
	}
	return user.ID, newConversationTestBearerToken(t, database, user.ID)
}

func newSyncTestVault(t *testing.T) *syncTestVault {
	t.Helper()
	database := openPresenceTestDatabase(t)
	service := browsersync.NewBrowserSyncService(database)
	router := chi.NewRouter()
	router.Get("/sync/vault", service.Vault())
	router.Post("/sync/vault", service.Vault())
	router.Delete("/sync/vault", service.Vault())
	router.Put("/sync/vault/key", service.VaultKey())
	router.Get("/sync/sessions", service.Sessions())
	router.Post("/sync/sessions", service.Sessions())
	router.Put("/sync/sessions/{sessionID}/name", service.SessionName())
	router.Post("/sync/sessions/{sessionID}/remove", service.SessionRemove())
	router.Get("/sync/usage", service.Usage())
	router.Post("/sync/ticket", service.Ticket())
	user, token := syncTestUser(t, database)
	public, root, _ := ed25519.GenerateKey(rand.Reader)
	sessionPublic, _, _ := ed25519.GenerateKey(rand.Reader)
	vaultID, sessionID := uuid.NewString(), uuid.NewString()
	if rec := performConversationRequest(t, router, "POST", "/sync/vault", token, map[string]any{
		"root_public_key": []byte(public), "key_envelope": syncTestEnvelope(),
		"session": syncTestGrant(vaultID, sessionID, sessionPublic, root), "name": "Laptop", "platform": "macos",
	}); rec.Code != 201 {
		t.Fatalf("create vault: %d %s", rec.Code, rec.Body)
	}
	return &syncTestVault{
		database: database, store: browsersync.NewStore(database.Conn), router: router, user: user, token: token,
		vaultID: vaultID, root: root,
		session: browsersync.SyncConnectionIdentity{UserID: user, VaultID: vaultID, SessionID: sessionID, PublicKey: sessionPublic},
	}
}

func syncOpaque(seed string) string {
	return strings.Repeat(seed, 64)[:64]
}

func syncWrite(scope, key string, base int64, body string) browsersync.SyncRecordWrite {
	sealed := make([]byte, 28)
	copy(sealed, body)
	return browsersync.SyncRecordWrite{Scope: syncOpaque(scope), Key: syncOpaque(key), BaseVersion: base, Sealed: sealed}
}

func TestAnAccountHasOneVault(t *testing.T) {
	v := newSyncTestVault(t)
	public, root, _ := ed25519.GenerateKey(rand.Reader)
	sessionPublic, _, _ := ed25519.GenerateKey(rand.Reader)
	rec := performConversationRequest(t, v.router, "POST", "/sync/vault", v.token, map[string]any{
		"root_public_key": []byte(public), "key_envelope": syncTestEnvelope(),
		"session": syncTestGrant(uuid.NewString(), uuid.NewString(), sessionPublic, root),
	})
	if rec.Code != 409 || !strings.Contains(rec.Body.String(), "sync_vault_exists") {
		t.Fatalf("a second vault: %d %s", rec.Code, rec.Body)
	}
	got := performConversationRequest(t, v.router, "GET", "/sync/vault", v.token, nil)
	if got.Code != 200 || !strings.Contains(got.Body.String(), v.vaultID) {
		t.Fatalf("read vault: %d %s", got.Code, got.Body)
	}
	_, strangerToken := syncTestUser(t, v.database)
	stranger := performConversationRequest(t, v.router, "GET", "/sync/vault", strangerToken, nil)
	if stranger.Code != 200 || strings.Contains(stranger.Body.String(), v.vaultID) {
		t.Fatalf("another account read this vault: %d %s", stranger.Code, stranger.Body)
	}
}

func TestTwoSessionsCreatingAVaultAtOnceLeaveExactlyOne(t *testing.T) {
	database := openPresenceTestDatabase(t)
	store := browsersync.NewStore(database.Conn)
	user, _ := syncTestUser(t, database)
	var wait sync.WaitGroup
	results := make([]error, 8)
	for i := range results {
		wait.Add(1)
		go func() {
			defer wait.Done()
			public, root, _ := ed25519.GenerateKey(rand.Reader)
			sessionPublic, _, _ := ed25519.GenerateKey(rand.Reader)
			results[i] = store.CreateVault(context.Background(), user, public, syncTestEnvelope(),
				syncTestGrant(uuid.NewString(), uuid.NewString(), sessionPublic, root), "", "", "")
		}()
	}
	wait.Wait()
	created := 0
	for _, err := range results {
		switch err {
		case nil:
			created++
		case browsersync.ErrSyncExists:
		default:
			t.Fatalf("unexpected: %v", err)
		}
	}
	if created != 1 {
		t.Fatalf("%d vaults were created", created)
	}
}

func TestASessionJoinsOnlyWithAGrantFromTheVaultRoot(t *testing.T) {
	v := newSyncTestVault(t)
	sessionPublic, _, _ := ed25519.GenerateKey(rand.Reader)
	_, forger, _ := ed25519.GenerateKey(rand.Reader)
	forged := performConversationRequest(t, v.router, "POST", "/sync/sessions", v.token, map[string]any{
		"session": syncTestGrant(v.vaultID, uuid.NewString(), sessionPublic, forger),
	})
	if forged.Code != 403 {
		t.Fatalf("a forged grant: %d %s", forged.Code, forged.Body)
	}
	good := performConversationRequest(t, v.router, "POST", "/sync/sessions", v.token, map[string]any{
		"session": syncTestGrant(v.vaultID, uuid.NewString(), sessionPublic, v.root), "name": "Desktop", "platform": "windows",
	})
	if good.Code != 201 {
		t.Fatalf("a real grant: %d %s", good.Code, good.Body)
	}
	listed := performConversationRequest(t, v.router, "GET", "/sync/sessions", v.token, nil)
	if !strings.Contains(listed.Body.String(), "Desktop") || !strings.Contains(listed.Body.String(), "Laptop") {
		t.Fatalf("sessions: %s", listed.Body)
	}
}

func TestRemovingASessionNeedsTheRootAndIsPermanent(t *testing.T) {
	v := newSyncTestVault(t)
	session := v.session.SessionID
	path := "/sync/sessions/" + session + "/remove"
	bytes, _ := json.Marshal([]any{"misty.sync.session-remove.v1", v.vaultID, session})
	_, forger, _ := ed25519.GenerateKey(rand.Reader)
	if rec := performConversationRequest(t, v.router, "POST", path, v.token, map[string]any{"proof": ed25519.Sign(forger, bytes)}); rec.Code != 403 {
		t.Fatalf("a login alone removed a session: %d %s", rec.Code, rec.Body)
	}
	if rec := performConversationRequest(t, v.router, "POST", path, v.token, map[string]any{"proof": ed25519.Sign(v.root, bytes)}); rec.Code != 204 {
		t.Fatalf("remove: %d %s", rec.Code, rec.Body)
	}
	ctx := context.Background()
	if _, err := v.store.PushRecords(ctx, v.session, []browsersync.SyncRecordWrite{syncWrite("a", "b", 0, "x")}); err != browsersync.ErrSyncForbidden {
		t.Fatalf("a removed session pushed: %v", err)
	}
	if err := v.store.CreateTicket(ctx, v.user, v.vaultID, session, strings.Repeat("0", 64), ""); err != browsersync.ErrSyncForbidden {
		t.Fatalf("a removed session got a ticket: %v", err)
	}
	if rec := performConversationRequest(t, v.router, "POST", "/sync/sessions", v.token, map[string]any{
		"session": syncTestGrant(v.vaultID, session, v.session.PublicKey, v.root),
	}); rec.Code != 403 {
		t.Fatalf("a removed session re-enrolled under the same id: %d %s", rec.Code, rec.Body)
	}
}

func TestRecordsAreCompareAndSwapAndIndependent(t *testing.T) {
	v := newSyncTestVault(t)
	ctx := context.Background()
	first, err := v.store.PushRecords(ctx, v.session, []browsersync.SyncRecordWrite{syncWrite("a", "1", 0, "one"), syncWrite("a", "2", 0, "two")})
	if err != nil || !first[0].Applied || first[0].Version != 1 || !first[1].Applied {
		t.Fatalf("first push: %+v %v", first, err)
	}
	// A stale write loses and is told the server's copy. The write beside it still applies.
	second, err := v.store.PushRecords(ctx, v.session, []browsersync.SyncRecordWrite{syncWrite("a", "1", 0, "stale"), syncWrite("a", "3", 0, "three")})
	if err != nil {
		t.Fatal(err)
	}
	if second[0].Applied || second[0].Version != 1 || second[0].Current == nil || !strings.HasPrefix(string(second[0].Current.Sealed), "one") {
		t.Fatalf("a stale write: %+v", second[0])
	}
	if !second[1].Applied {
		t.Fatalf("the independent write was held back: %+v", second[1])
	}
	page, err := v.store.PullRecords(ctx, v.session, 0)
	if err != nil || len(page.Records) != 3 || page.Cursor != 3 || page.Head != 3 || page.More {
		t.Fatalf("pull: %+v %v", page, err)
	}
	after, err := v.store.PullRecords(ctx, v.session, 2)
	if err != nil || len(after.Records) != 1 || after.Records[0].Key != syncOpaque("3") {
		t.Fatalf("pull after a cursor: %+v %v", after, err)
	}
}

func TestAPageFitsInAFrameAndAlwaysMovesForward(t *testing.T) {
	v := newSyncTestVault(t)
	ctx := context.Background()
	big := make([]byte, browsersync.SyncMaxRecordBytes)
	for batch := 0; batch < 6; batch++ {
		key := string(rune('a' + batch))
		if _, err := v.store.PushRecords(ctx, v.session, []browsersync.SyncRecordWrite{{Scope: syncOpaque("0"), Key: syncOpaque(key), Sealed: big}}); err != nil {
			t.Fatal(err)
		}
	}
	cursor, seen, pages := int64(0), 0, 0
	for {
		page, err := v.store.PullRecords(ctx, v.session, cursor)
		if err != nil {
			t.Fatal(err)
		}
		total := 0
		for _, record := range page.Records {
			total += len(record.Sealed)
		}
		if total > browsersync.SyncPageBytes {
			t.Fatalf("a page of %d bytes", total)
		}
		if page.Cursor <= cursor && page.More {
			t.Fatalf("a pull didn't move: %d -> %d", cursor, page.Cursor)
		}
		seen += len(page.Records)
		cursor = page.Cursor
		pages++
		if !page.More {
			break
		}
	}
	if seen != 6 || pages < 2 {
		t.Fatalf("saw %d records in %d pages", seen, pages)
	}
}

func TestWritesAreValidatedBeforeAnythingApplies(t *testing.T) {
	v := newSyncTestVault(t)
	ctx := context.Background()
	for name, write := range map[string]browsersync.SyncRecordWrite{
		"short":     {Scope: syncOpaque("a"), Key: syncOpaque("b"), Sealed: []byte("x")},
		"oversized": {Scope: syncOpaque("a"), Key: syncOpaque("b"), Sealed: make([]byte, browsersync.SyncMaxRecordBytes+1)},
		"scope":     {Scope: "bookmarks", Key: syncOpaque("b"), Sealed: make([]byte, 28)},
		"key":       {Scope: syncOpaque("a"), Key: "plain", Sealed: make([]byte, 28)},
		"version":   {Scope: syncOpaque("a"), Key: syncOpaque("b"), BaseVersion: -1, Sealed: make([]byte, 28)},
		"lifetime":  {Scope: syncOpaque("a"), Key: syncOpaque("b"), Sealed: make([]byte, 28), ExpiresIn: 90000},
	} {
		if _, err := v.store.PushRecords(ctx, v.session, []browsersync.SyncRecordWrite{syncWrite("c", "d", 0, "good"), write}); err != browsersync.ErrSyncInvalid {
			t.Fatalf("%s: %v", name, err)
		}
	}
	page, _ := v.store.PullRecords(ctx, v.session, 0)
	if len(page.Records) != 0 {
		t.Fatalf("a refused frame applied %d writes", len(page.Records))
	}
}

func TestAnyScopeWorksSoNewKindsOfDataNeedNoMigration(t *testing.T) {
	v := newSyncTestVault(t)
	ctx := context.Background()
	for _, scope := range []string{"1", "2", "3", "4", "5", "6", "7", "8", "9", "a", "b", "c"} {
		if _, err := v.store.PushRecords(ctx, v.session, []browsersync.SyncRecordWrite{syncWrite(scope, "1", 0, scope)}); err != nil {
			t.Fatalf("scope %s: %v", scope, err)
		}
	}
	usage, err := v.store.Usage(ctx, v.user)
	if err != nil || len(usage.Scopes) != 12 || usage.Bytes != 12*28 || usage.Limit != nil {
		t.Fatalf("usage: %+v %v", usage, err)
	}
}

func TestExpiredRecordsLeaveAndACursorBehindThemGetsAFullListing(t *testing.T) {
	v := newSyncTestVault(t)
	ctx := context.Background()
	keep := syncWrite("a", "1", 0, "keep")
	clip := syncWrite("a", "2", 0, "clip")
	clip.ExpiresIn = 1
	if _, err := v.store.PushRecords(ctx, v.session, []browsersync.SyncRecordWrite{clip, keep}); err != nil {
		t.Fatal(err)
	}
	if _, err := v.database.Conn.ExecContext(ctx, `UPDATE sync_records SET expires_at=now()-interval '1 hour' WHERE vault_id=$1 AND record_key=$2`, v.vaultID, clip.Key); err != nil {
		t.Fatal(err)
	}
	page, err := v.store.PullRecords(ctx, v.session, 0)
	if err != nil || len(page.Records) != 1 || page.Records[0].Key != keep.Key {
		t.Fatalf("an expired record was served: %+v %v", page, err)
	}
	if _, err = v.store.PushRecords(ctx, v.session, []browsersync.SyncRecordWrite{syncWrite("a", "3", 0, "more")}); err != nil {
		t.Fatal(err)
	}
	reset, err := v.store.PullRecords(ctx, v.session, 0)
	if err != nil || !reset.Reset {
		t.Fatalf("a cursor behind compaction: %+v %v", reset, err)
	}
	var left int
	if err = v.database.Conn.QueryRowContext(ctx, `SELECT count(*) FROM sync_records WHERE vault_id=$1 AND record_key=$2`, v.vaultID, clip.Key).Scan(&left); err != nil || left != 0 {
		t.Fatalf("compaction left %d rows: %v", left, err)
	}
}

func TestDestroyingAVaultNeedsProof(t *testing.T) {
	v := newSyncTestVault(t)
	destroy := func(body map[string]any) int {
		return performConversationRequest(t, v.router, "DELETE", "/sync/vault", v.token, body).Code
	}
	_, forger, _ := ed25519.GenerateKey(rand.Reader)
	bytes, _ := json.Marshal([]any{"misty.sync.destroy.v1", v.vaultID})
	if code := destroy(map[string]any{"proof": ed25519.Sign(forger, bytes)}); code != 412 {
		t.Fatalf("a forged proof: %d", code)
	}
	if code := destroy(map[string]any{"current_password": "wrong"}); code != 412 {
		t.Fatalf("a wrong password: %d", code)
	}
	if _, err := v.store.PushRecords(context.Background(), v.session, []browsersync.SyncRecordWrite{syncWrite("a", "1", 0, "x")}); err != nil {
		t.Fatal(err)
	}
	if code := destroy(map[string]any{"proof": ed25519.Sign(v.root, bytes)}); code != 200 {
		t.Fatalf("a real proof: %d", code)
	}
	var left int
	if err := v.database.Conn.QueryRowContext(context.Background(), `SELECT count(*) FROM sync_records WHERE vault_id=$1`, v.vaultID).Scan(&left); err != nil || left != 0 {
		t.Fatalf("%d records survived the vault: %v", left, err)
	}
}
