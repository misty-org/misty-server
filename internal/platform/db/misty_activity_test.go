package db

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
)

// Uses a disposable schema, real PostgreSQL query execution and both personal
// representations (NULL/empty); no application's user data is truncated.
func TestMistyActivityPersonalScope(t *testing.T) {
	database, _ := syncTestDatabase(t)
	_, err := database.Conn.Exec(`
 CREATE TABLE spaces(id text primary key, lifecycle_state text);
 CREATE TABLE space_members(space_id text,user_id text,role text);
 CREATE TABLE ai_invocations(id text,user_id text,space_id text,state text,request_payload jsonb,conversation_id text,agent_run_id text,updated_at timestamptz);
 CREATE TABLE ai_invocation_events(invocation_id text,sequence bigint,event_type text,payload jsonb);
 CREATE TABLE misty_agent_execution_leases(owner_user_id text,agent_id text,task_id text,expires_at timestamptz);
 CREATE TABLE space_runs(id text,owner_user_id text,space_id text,state text,agent_id text,input jsonb,parent_run_id text,delegation_depth int,result jsonb,updated_at timestamptz);
 INSERT INTO spaces VALUES('history','active');
 INSERT INTO space_members VALUES('history','owner','owner');
 INSERT INTO ai_invocations VALUES
 ('personal-null','owner',NULL,'completed','{"agent_id":"one","prompt":"Personal"}','',NULL,NOW()),
 ('personal-empty','owner','','running','{"agent_id":"two","execution_mode":"agent","task_id":"gone"}','',NULL,NOW()),
 ('other-account','other',NULL,'completed','{"agent_id":"one"}','',NULL,NOW()),
 ('historical','owner','history','completed','{"agent_id":"one"}','',NULL,NOW());
 INSERT INTO space_runs VALUES('delegated','owner',NULL,'completed','one','{"parent_invocation_id":"personal-null"}',NULL,1,'{}',NOW());
 `)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	read := func(user, space, agent string) map[string]string {
		t.Helper()
		raw, err := database.MistyActivity(ctx, user, space, agent)
		if err != nil {
			t.Fatal(err)
		}
		var entries []struct {
			ID    string `json:"id"`
			State string `json:"state"`
		}
		if err = json.Unmarshal(raw, &entries); err != nil {
			t.Fatal(err)
		}
		result := map[string]string{}
		for _, entry := range entries {
			result[entry.ID] = entry.State
		}
		return result
	}
	personal := read("owner", "", "")
	if len(personal) != 4 || personal["personal-null"] != "completed" || personal["personal-empty"] != "paused" || personal["delegated"] != "completed" {
		t.Fatalf("incorrect personal activity: %#v", personal)
	}
	filtered := read("owner", "", "one")
	if len(filtered) != 3 || filtered["personal-empty"] != "" {
		t.Fatalf("agent filter: %#v", filtered)
	}
	if rows := read("other", "", ""); len(rows) != 1 || rows["other-account"] != "completed" {
		t.Fatalf("cross-account leak: %#v", rows)
	}
	if rows := read("owner", "history", ""); len(rows) != 4 || rows["historical"] != "completed" {
		t.Fatalf("history filter: %#v", rows)
	}
	if rows := read("other", "history", ""); len(rows) != 1 || rows["other-account"] != "completed" {
		t.Fatalf("legacy destination changed account history: %#v", rows)
	}
	if _, err = database.MistyActivity(ctx, "", ""); !errors.Is(err, ErrSpaceForbidden) {
		t.Fatalf("missing identity: %v", err)
	}
}
