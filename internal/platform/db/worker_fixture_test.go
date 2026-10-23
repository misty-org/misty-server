package db

import (
	"context"
	"database/sql"
	"os"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/lib/pq"
)

func workerTestDatabase(t *testing.T) *Database {
	t.Helper()
	if os.Getenv("MISTY_WORKER_TEST") != "1" || os.Getenv("DB_NAME") != "misty_worker_test" {
		t.Skip("requires disposable misty_worker_test PostgreSQL")
	}
	database := &Database{}
	conn, err := sql.Open("postgres", database.GetDSN())
	if err != nil {
		t.Fatal(err)
	}
	database.Conn = conn
	conn.SetMaxOpenConns(1)
	var name string
	if err := conn.QueryRow("SELECT current_database()").Scan(&name); err != nil || name != "misty_worker_test" {
		t.Fatal("refusing non-test database", err)
	}
	schema := "worker_" + strings.ReplaceAll(uuid.NewString(), "-", "")
	if _, err = conn.Exec("CREATE SCHEMA " + pq.QuoteIdentifier(schema) + "; SET search_path TO " + pq.QuoteIdentifier(schema)); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { conn.Exec("DROP SCHEMA " + pq.QuoteIdentifier(schema) + " CASCADE"); database.Stop() })
	_, err = conn.Exec(`
 CREATE TABLE library_processing_jobs(
  id text PRIMARY KEY,job_kind text,state text DEFAULT 'queued',target_id text,
  available_at timestamptz DEFAULT now(),lease_expires_at timestamptz,priority int DEFAULT 0,created_at timestamptz DEFAULT now(),
  updated_at timestamptz DEFAULT now(),lease_owner text,lease_token text,attempt_count int DEFAULT 0,
  security_domain_id text DEFAULT 'domain',space_id text DEFAULT 'space',payload jsonb DEFAULT '{}',billing_user_id text);
 CREATE TABLE library_blobs(id text PRIMARY KEY,lifecycle_state text DEFAULT 'ready',r2_object_key text DEFAULT 'object',
  server_detected_mime_type text DEFAULT 'image/png',byte_size bigint DEFAULT 10,sha256 text DEFAULT 'digest');
 CREATE TABLE library_files(id text PRIMARY KEY,lifecycle_state text DEFAULT 'ready',blob_id text,original_filename text DEFAULT 'image.png');
 CREATE TABLE space_library_items(id text PRIMARY KEY,lifecycle_state text DEFAULT 'ready',file_id text,space_id text DEFAULT 'space',
  added_by_user_id text DEFAULT 'owner',display_name text DEFAULT 'image',caption text DEFAULT '',tags jsonb DEFAULT '[]');
 CREATE TABLE space_library_intelligence_policies(space_id text PRIMARY KEY);
 CREATE TABLE library_item_versions(id text PRIMARY KEY,lifecycle_state text DEFAULT 'ready',space_library_item_id text,
  edit_definition jsonb DEFAULT '{}',rendition_state text,rendition_updated_at timestamptz);
 CREATE TABLE space_rendition_reservations(source_kind text,source_id text,state text,reserved_bytes bigint DEFAULT 100,user_id text DEFAULT 'owner');
 CREATE TABLE space_note_control_outbox(id text PRIMARY KEY,next_attempt_at timestamptz,delivered_at timestamptz);
 CREATE TABLE space_drawing_control_outbox(id text PRIMARY KEY,drawing_id text,command text,next_attempt_at timestamptz,delivered_at timestamptz);
 CREATE TABLE space_drawings(id text PRIMARY KEY,lifecycle_state text);
 CREATE TABLE social_outbound_commands(id text PRIMARY KEY,state text,available_at timestamptz,lease_expires_at timestamptz,binding_id text);
 CREATE TABLE social_scheduled_messages(id text PRIMARY KEY,status text,scheduled_at timestamptz,authority_id text);
 CREATE TABLE social_send_authorities(id text PRIMARY KEY,allow_scheduled bool,revoked_at timestamptz);
 CREATE TABLE social_bindings(id text PRIMARY KEY,status text,disabled_at timestamptz);
 CREATE TABLE billing_adapter_outbox(id text PRIMARY KEY,available_at timestamptz,delivered_at timestamptz);
 CREATE TABLE billing_adapter_intents(id text PRIMARY KEY,state text,expires_at timestamptz);
 CREATE TABLE billing_adapter_reservations(account_id text,reservation_id text);
 CREATE TABLE voice_usage_journal(id text PRIMARY KEY,account_id text,reservation_id text,state text,updated_at timestamptz);
 CREATE TABLE ai_retrieval_documents(id text PRIMARY KEY,owner_user_id text,lifecycle_state text);
 CREATE TABLE ai_retrieval_chunks(document_id text,ordinal int,embedding text,embedding_lease_until timestamptz);
 INSERT INTO library_blobs(id) VALUES('blob');
 INSERT INTO library_files(id,blob_id) VALUES('file','blob');
 INSERT INTO space_library_items(id,file_id) VALUES('item','file');
 INSERT INTO library_item_versions(id,space_library_item_id) VALUES('edit','item');
 INSERT INTO space_rendition_reservations(source_kind,source_id,state) VALUES('edit','edit','active');`)
	if err != nil {
		t.Fatal(err)
	}
	migration, err := migrationFiles.ReadFile("migrations/20271001030000_worker_notifications.sql")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = conn.ExecContext(context.Background(), strings.Split(string(migration), "-- +goose Down")[0]); err != nil {
		t.Fatal(err)
	}
	return database
}
