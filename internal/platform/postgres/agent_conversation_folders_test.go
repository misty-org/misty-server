package db

import (
	"context"
	"errors"
	"os"
	"strings"
	"testing"
)

func conversationFolderTestDatabase(t *testing.T) *Database {
	t.Helper()
	database, _ := syncTestDatabase(t)
	_, err := database.Conn.Exec(`CREATE FUNCTION misty_rls_is_service() RETURNS boolean LANGUAGE sql AS 'SELECT false';
 CREATE FUNCTION misty_rls_user_id() RETURNS text LANGUAGE sql AS 'SELECT current_setting(''app.current_user_id'',true)';
 CREATE FUNCTION misty_notify_account_change() RETURNS trigger LANGUAGE plpgsql AS 'BEGIN RETURN NULL; END';
 CREATE TABLE misty_ask_identities(id text PRIMARY KEY, owner_user_id text NOT NULL);
 CREATE TABLE misty_ask_conversations(id text PRIMARY KEY, user_id text NOT NULL, agent_id text, deleted_at timestamptz);
 INSERT INTO misty_ask_identities VALUES ('agent-a','owner'),('agent-b','owner'),('agent-other','other');
 INSERT INTO misty_ask_conversations VALUES ('chat-a','owner','agent-a',NULL),('chat-b','owner','agent-b',NULL);`)
	if err != nil {
		t.Fatal(err)
	}
	migration, err := os.ReadFile("migrations/20271008010000_agent_conversation_folders.sql")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = database.Conn.Exec(strings.Split(string(migration), "-- +goose Down")[0]); err != nil {
		t.Fatal(err)
	}
	return database
}

func TestAgentConversationFoldersStayWithTheirAgent(t *testing.T) {
	database := conversationFolderTestDatabase(t)
	ctx := context.Background()

	if _, err := database.CreateAgentConversationFolder(ctx, "owner", "agent-other", "Trips"); !errors.Is(err, ErrConversationFolderNotFound) {
		t.Fatalf("created a folder on another account's agent: %v", err)
	}
	folder, err := database.CreateAgentConversationFolder(ctx, "owner", "agent-a", "  Trips ")
	if err != nil {
		t.Fatal(err)
	}
	if folder.Name != "Trips" || folder.AgentID != "agent-a" || !strings.HasPrefix(folder.ID, "folder_") {
		t.Fatalf("unexpected folder %+v", folder)
	}

	if err = database.FileAgentConversation(ctx, "owner", "chat-b", folder.ID); !errors.Is(err, ErrConversationFolderNotFound) {
		t.Fatalf("filed another agent's chat into the folder: %v", err)
	}
	if err = database.FileAgentConversation(ctx, "other", "chat-a", folder.ID); !errors.Is(err, ErrConversationFolderNotFound) {
		t.Fatalf("another account filed the chat: %v", err)
	}
	if err = database.FileAgentConversation(ctx, "owner", "chat-a", folder.ID); err != nil {
		t.Fatal(err)
	}

	if _, err = database.RenameAgentConversationFolder(ctx, "other", folder.ID, "Mine"); !errors.Is(err, ErrConversationFolderNotFound) {
		t.Fatalf("another account renamed the folder: %v", err)
	}
	renamed, err := database.RenameAgentConversationFolder(ctx, "owner", folder.ID, "Travel")
	if err != nil || renamed.Name != "Travel" {
		t.Fatalf("rename: %+v %v", renamed, err)
	}

	if err = database.DeleteAgentConversationFolder(ctx, "owner", folder.ID); err != nil {
		t.Fatal(err)
	}
	var filed *string
	if err = database.Conn.QueryRow(`SELECT folder_id FROM misty_ask_conversations WHERE id='chat-a'`).Scan(&filed); err != nil {
		t.Fatal(err)
	}
	if filed != nil {
		t.Fatalf("deleting the folder kept the chat filed in %q", *filed)
	}
	folders, err := database.ListAgentConversationFolders(ctx, "owner")
	if err != nil || len(folders) != 0 {
		t.Fatalf("folders after delete: %+v %v", folders, err)
	}
}
