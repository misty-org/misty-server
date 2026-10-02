package api

import (
	"context"
	"database/sql"
	"encoding/json"
	"os"
	"strings"
	"testing"

	"github.com/google/uuid"
	db "github.com/kannachi323/misty/server/internal/platform/postgres"
	"github.com/lib/pq"
)

func testRealtimeClient(s *RealtimeService, user string, capacity int) *TestingRealtimeClient {
	c := &TestingRealtimeClient{TestingUserID: user, TestingSend: make(chan []byte, capacity), TestingDone: make(chan struct{})}
	s.register(c)
	return c
}

func TestRealtimeResetAndOverflow(t *testing.T) {
	s := NewRealtimeService(nil, "")
	healthy := testRealtimeClient(s, "a", 2)
	slow := testRealtimeClient(s, "a", 1)
	slow.TestingSend <- []byte(`old`)
	s.broadcastReset()
	if got := string(<-healthy.TestingSend); got != string(realtimeReset) {
		t.Fatalf("reset = %s", got)
	}
	select {
	case <-slow.TestingDone:
	default:
		t.Fatal("full queue must force recovery, not drop reset")
	}
	if s.ConnectionCount() != 1 || len(s.clientsByUser["a"]) != 1 {
		t.Fatal("stale client index")
	}
	s.TestingUnregister(healthy)
	if len(s.clientsByUser) != 0 {
		t.Fatal("empty account retained")
	}
}

func TestRealtimeFanoutPreservesAccountAndResourceBoundaries(t *testing.T) {
	dsn := os.Getenv("MISTY_TRAFFIC_TEST_DSN")
	if dsn == "" {
		t.Skip("requires disposable misty_traffic_test PostgreSQL")
	}
	conn, err := sql.Open("postgres", dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	conn.SetMaxOpenConns(1)
	var name string
	if err := conn.QueryRow("SELECT current_database()").Scan(&name); err != nil || name != "misty_traffic_test" {
		t.Fatal("refusing non-test database", err)
	}
	schema := "traffic_" + strings.ReplaceAll(uuid.NewString(), "-", "")
	if _, err = conn.Exec("CREATE SCHEMA " + pq.QuoteIdentifier(schema) + "; SET search_path TO " + pq.QuoteIdentifier(schema)); err != nil {
		t.Fatal(err)
	}
	defer conn.Exec("DROP SCHEMA " + pq.QuoteIdentifier(schema) + " CASCADE")
	_, err = conn.Exec(`
 CREATE TABLE space_members (space_id text, user_id text, role text DEFAULT 'member');
 CREATE TABLE space_member_permission_overrides (space_id text, user_id text, permission text, effect text);
 CREATE TABLE spaces (id text, lifecycle_state text DEFAULT 'active');
 INSERT INTO spaces(id) VALUES ('s'), ('elsewhere');
 CREATE TABLE space_events (id bigint, space_id text, event_type text, actor_user_id text, entity_id text, payload jsonb, created_at timestamptz);
 CREATE TABLE space_conversations (id text, space_id text);
 CREATE TABLE space_conversation_members (conversation_id text, user_id text);
 INSERT INTO space_members(space_id,user_id) VALUES ('s','member'), ('elsewhere','outsider');
 INSERT INTO space_events VALUES (1,'s','space.updated',NULL,NULL,'{}',now()),
  (2,'s','message.created',NULL,NULL,'{"conversation_id":"private"}',now());
 INSERT INTO space_conversations VALUES ('private','s');`)
	if err != nil {
		t.Fatal(err)
	}
	database := &db.Database{Conn: conn}
	s := NewRealtimeService(database, "")
	first := testRealtimeClient(s, "member", 4)
	second := testRealtimeClient(s, "member", 4)
	outsider := testRealtimeClient(s, "outsider", 4)
	s.BroadcastEvent(1)
	one, two := <-first.TestingSend, <-second.TestingSend
	if string(one) != string(two) || &one[0] != &two[0] {
		t.Fatal("devices should share the authorized encoded event")
	}
	var envelope struct {
		Type string `json:"type"`
	}
	if json.Unmarshal(one, &envelope) != nil || envelope.Type != "event" {
		t.Fatalf("not an event: %s", one)
	}
	if _, err := conn.Exec(`INSERT INTO space_members(space_id,user_id) VALUES ('s','participant'); INSERT INTO space_conversation_members VALUES ('private','participant')`); err != nil {
		t.Fatal(err)
	}
	participant := testRealtimeClient(s, "participant", 4)
	s.BroadcastEvent(2) // Space membership does not grant access to a private conversation.
	if len(first.TestingSend) != 0 || len(second.TestingSend) != 0 || len(outsider.TestingSend) != 0 {
		t.Fatal("unauthorized event delivered")
	}
	// One authorization pass resolves each recipient separately.
	if got := <-participant.TestingSend; !strings.Contains(string(got), `"type":"event"`) {
		t.Fatalf("participant did not receive the private event: %s", got)
	}
	if _, err := conn.Exec(`DELETE FROM space_members WHERE user_id IN ('member','participant')`); err != nil {
		t.Fatal(err)
	}
	candidates, err := database.SpaceEventCandidateUsers(context.Background(), 1)
	if err != nil || len(candidates) != 0 {
		t.Fatal("candidate membership was cached", candidates, err)
	}
	s.BroadcastEvent(1)
	if len(first.TestingSend) != 0 || len(second.TestingSend) != 0 || len(participant.TestingSend) != 0 {
		t.Fatal("revoked member received event")
	}
}
