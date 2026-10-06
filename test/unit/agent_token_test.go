package unit

import (
	"encoding/base64"
	"strings"
	"testing"
	"time"

	"github.com/kannachi323/misty/server/internal/platform/security"
)

func agentTokenSigners(t *testing.T, current, previous string) (*security.SessionSigner, *security.AgentTokenSigner) {
	t.Helper()
	t.Setenv("MISTY_AUTH_SIGNING_KEY", base64.StdEncoding.EncodeToString([]byte(strings.Repeat(current, 32))))
	t.Setenv("MISTY_AUTH_SIGNING_KEY_PREVIOUS", "")
	if previous != "" {
		t.Setenv("MISTY_AUTH_SIGNING_KEY_PREVIOUS", base64.StdEncoding.EncodeToString([]byte(strings.Repeat(previous, 32))))
	}
	sessions, err := security.SessionSignerFromEnv()
	if err != nil {
		t.Fatal(err)
	}
	agents, err := sessions.AgentTokenSigner()
	if err != nil {
		t.Fatal(err)
	}
	return sessions, agents
}

func TestAgentTokenBindsUserAgentAndSession(t *testing.T) {
	sessions, agents := agentTokenSigners(t, "a", "")
	token, expires, err := agents.Mint("alice", "agent-1", "session-1", time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if until := time.Until(expires); until <= 0 || until > security.AgentTokenTTL {
		t.Fatalf("token lifetime = %s", until)
	}
	claims, err := agents.Verify(token, "alice", "session-1")
	if err != nil || claims.AgentID != "agent-1" {
		t.Fatalf("valid token rejected: %v", err)
	}
	if strings.Contains(token, "session-1") {
		t.Fatal("token carries the raw session id")
	}
	if _, err := agents.Verify(token, "bob", "session-1"); err == nil {
		t.Fatal("token accepted for another member")
	}
	if _, err := agents.Verify(token, "alice", "session-2"); err == nil {
		t.Fatal("token accepted from another session")
	}
	if _, err := agents.Verify("", "alice", "session-1"); err == nil {
		t.Fatal("empty token accepted")
	}
	// Session and agent tokens share a root key but never verify as each other.
	access, err := sessions.Mint("alice", "session-1", "access", time.Now().Add(time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := agents.Verify(access, "alice", "session-1"); err == nil {
		t.Fatal("session token accepted as an agent token")
	}
	if _, err := sessions.Verify(token, "access"); err == nil {
		t.Fatal("agent token accepted as a session token")
	}
	if _, err := agents.Verify(token[:len(token)-2]+"xx", "alice", "session-1"); err == nil {
		t.Fatal("tampered signature accepted")
	}
}

func TestAgentTokenLifetimeAndRotation(t *testing.T) {
	_, old := agentTokenSigners(t, "a", "")
	expired, _, err := old.Mint("alice", "agent-1", "session-1", time.Now().Add(-10*time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := old.Verify(expired, "alice", "session-1"); err == nil {
		t.Fatal("expired token accepted")
	}
	current, _, err := old.Mint("alice", "agent-1", "session-1", time.Now())
	if err != nil {
		t.Fatal(err)
	}
	_, rotated := agentTokenSigners(t, "b", "a")
	if _, err := rotated.Verify(current, "alice", "session-1"); err != nil {
		t.Fatalf("token from the previous key rejected during rotation: %v", err)
	}
	_, replaced := agentTokenSigners(t, "c", "")
	if _, err := replaced.Verify(current, "alice", "session-1"); err == nil {
		t.Fatal("token from a retired key accepted")
	}
}
