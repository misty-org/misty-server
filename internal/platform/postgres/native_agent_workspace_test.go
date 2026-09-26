package db

import (
	"context"
	"errors"
	"reflect"
	"testing"
)

func TestAgentWorkspaceToolsWithoutInstallations(t *testing.T) {
	database, _ := syncTestDatabase(t)
	// Deliberately no installation, assignment, or Space tables.
	_, err := database.Conn.Exec(`
        CREATE TABLE misty_ask_identities(id text primary key, owner_user_id text, enabled boolean, deleted_at timestamptz);
        INSERT INTO misty_ask_identities VALUES
        ('active','owner',true,NULL), ('disabled','owner',false,NULL),
        ('deleted','owner',true,NOW()), ('foreign','other',true,NULL);`)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	tools, err := database.AgentWorkspaceTools(ctx, "owner", "active")
	if err != nil || !reflect.DeepEqual(tools, []string{"browser", "files"}) {
		t.Fatalf("built-ins: %v, %v", tools, err)
	}
	for _, agent := range []string{"disabled", "deleted", "foreign", "missing", ""} {
		tools, err := database.AgentWorkspaceTools(ctx, "owner", agent)
		if !errors.Is(err, ErrPersonalAgentNotFound) || len(tools) != 0 {
			t.Errorf("%s obtained tools: %v, %v", agent, tools, err)
		}
	}
	if tools, err := database.AgentWorkspaceTools(ctx, "", "active"); !errors.Is(err, ErrPersonalAgentNotFound) || len(tools) != 0 {
		t.Fatalf("missing account: %v, %v", tools, err)
	}
}
