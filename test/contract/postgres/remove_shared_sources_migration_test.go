package db

import (
	"context"
	"database/sql"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/google/uuid"
)

// Reconstruct only the retired tables inside a rollback-only transaction. This
// tests real migration SQL with mixed data, not a simulated deletion algorithm.
func TestRemoveSharedSourcesPreservesNativeContentAndAccounts(t *testing.T) {
	database := openTestDatabase(t)
	ctx := context.Background()
	user, err := database.CreateUser("Removal test", "remove-sources@example.invalid", "password123")
	if err != nil {
		t.Fatal(err)
	}
	space, err := database.CreateSpace(ctx, user.ID, "Preserved Space")
	if err != nil {
		t.Fatal(err)
	}
	ask, err := database.EnsureAskIdentity(ctx, user.ID, "google/gemini-2.5-flash-lite")
	if err != nil {
		t.Fatal(err)
	}
	_, file, _, _ := runtime.Caller(0)
	migration := func(name string) string {
		t.Helper()
		raw, err := os.ReadFile(filepath.Join(filepath.Dir(file), "../../../internal/platform/postgres/migrations", name))
		if err != nil {
			t.Fatal(err)
		}
		return strings.Split(strings.Split(string(raw), "-- +goose Up")[1], "-- +goose Down")[0]
	}
	rollback := errors.New("rollback migration fixture")
	connection := "connection_" + uuid.NewString()
	err = database.TestingSpaceTx(ctx, func(tx *sql.Tx) error {
		exec := func(query string, args ...any) {
			t.Helper()
			if _, err := tx.ExecContext(ctx, query, args...); err != nil {
				t.Fatal(err)
			}
		}
		for _, name := range []string{"20270118000000_provider_source_subscriptions.sql", "20270122000000_provider_source_watches.sql"} {
			exec(migration(name))
		}
		exec(`INSERT INTO space_source_items(id,space_id,destination,provider,contributor_id,resource) VALUES('retired',$1,'journal','google-docs',$2,'{}')`, space.ID, user.ID)
		exec(`INSERT INTO provider_source_previews(token_hash,user_id,space_id,payload,expires_at) VALUES('preview',$1,$2,'{}',NOW()+INTERVAL '1 hour')`, user.ID, space.ID)
		exec(`INSERT INTO provider_source_subscriptions(id,item_id,owner_id,space_id,destination,source_key,source,executor) VALUES('subscription','retired',$1,$2,'journal','key','{}','none')`, user.ID, space.ID)
		exec(`INSERT INTO space_notes(id,space_id,creator_user_id,title_projection,audience_kind) VALUES('preserved-copy',$1,$2,'Independent note','space')`, space.ID, user.ID)
		exec(`INSERT INTO provider_source_copies(request_id,user_id,source_id,native_id,source_version) VALUES($1,$2,'retired','preserved-copy',1)`, uuid.NewString(), user.ID)
		exec(`INSERT INTO space_storage_contributions(id,space_id,user_id,source_kind,source_id,logical_bytes,state) VALUES('retired-charge',$1,$2,'import','retired',100,'active'),('keep-charge',$1,$2,'import','ordinary-import',200,'active')`, space.ID, user.ID)
		exec(`INSERT INTO space_storage_usage(space_id,used_bytes) VALUES($1,300) ON CONFLICT(space_id) DO UPDATE SET used_bytes=300`, space.ID)
		exec(`INSERT INTO connected_accounts(id,user_id,provider,account_id,credential_ciphertext,credential_nonce) VALUES($1,$2,'figma','fixture','x','x')`, connection, user.ID)
		exec(`INSERT INTO space_integrations(id,space_id,provider,display_name,credential_reference,connected_by_user_id) VALUES('keep-integration',$1,'figma','Figma','fixture',$2)`, space.ID, user.ID)
		exec(`INSERT INTO provider_shared_resources(id,space_id,integration_id,published_by_user_id,provider,resource_type,external_resource_id,display_name,permission_scope) VALUES('retired-publication',$1,'keep-integration',$2,'figma','file','old','Old publication','fixture'),('keep-binding-resource',$1,'keep-integration',$2,'figma','file','file','Bound file','fixture')`, space.ID, user.ID)
		exec(`INSERT INTO figma_space_bindings(id,space_id,connection_id,integration_id,shared_resource_id,bound_by_user_id,resource_type,external_id,display_name,file_key) VALUES('keep-binding',$1,$2,'keep-integration','keep-binding-resource',$3,'file','file','File','file')`, space.ID, connection, user.ID)
		exec(`INSERT INTO user_app_installations(user_id,app_id,installed_version,granted_scopes) VALUES($1,'journal','0.1.0','["sources.read","sources.write","files.read"]') ON CONFLICT(user_id,app_id) DO UPDATE SET granted_scopes=EXCLUDED.granted_scopes`, user.ID)
		exec(migration("20270201000000_remove_shared_sources.sql"))
		for _, name := range []string{"space_source_items", "provider_source_previews", "provider_source_subscriptions", "provider_source_copies", "provider_source_watches"} {
			var missing bool
			if err := tx.QueryRowContext(ctx, `SELECT to_regclass($1) IS NULL`, name).Scan(&missing); err != nil || !missing {
				t.Fatalf("retired table %s still exists: %v", name, err)
			}
		}
		var ok bool
		if err := tx.QueryRowContext(ctx, `SELECT
   (SELECT used_bytes=200 FROM space_storage_usage WHERE space_id=$1)
   AND NOT EXISTS(SELECT 1 FROM space_storage_contributions WHERE id='retired-charge')
   AND EXISTS(SELECT 1 FROM space_storage_contributions WHERE id='keep-charge')
   AND EXISTS(SELECT 1 FROM space_notes WHERE id='preserved-copy')
   AND EXISTS(SELECT 1 FROM connected_accounts WHERE id=$2)
   AND EXISTS(SELECT 1 FROM misty_ask_identities WHERE id=$3)
   AND EXISTS(SELECT 1 FROM figma_space_bindings WHERE id='keep-binding')
   AND NOT EXISTS(SELECT 1 FROM provider_shared_resources WHERE id='retired-publication')
   AND (SELECT granted_scopes='["files.read"]'::jsonb FROM user_app_installations WHERE user_id=$4 AND app_id='journal')`, space.ID, connection, ask.ID, user.ID).Scan(&ok); err != nil || !ok {
			t.Fatalf("selective cleanup failed: %v", err)
		}
		return rollback
	})
	if !errors.Is(err, rollback) {
		t.Fatal(err)
	}
}
