package api

import (
	"bufio"
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	. "github.com/kannachi323/misty/server/internal/platform/httpapi"
	"github.com/kannachi323/misty/server/internal/platform/security"
)

// The account stream stays open across revalidations while its session is
// active, and ends once the session is revoked, without periodic reconnects.
func TestAccountEventsRevalidateSessionWithoutReconnecting(t *testing.T) {
	database := openPresenceTestDatabase(t)
	restore := TestingSetAccountEventSessionCheck(100 * time.Millisecond)
	defer restore()
	user, err := database.CreateUser("Stream", "account-stream-session@example.invalid", "password123")
	if err != nil {
		t.Fatal(err)
	}
	sid, refresh := "sid-"+uniqueTestEmail("stream"), "refresh-"+uniqueTestEmail("stream")
	if err := database.CreateRefreshSession(context.Background(), security.HashToken(sid), security.HashToken(refresh), user.ID, time.Now().Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	signer, err := security.SessionSignerFromEnv()
	if err != nil {
		t.Fatal(err)
	}
	access, err := signer.Mint(user.ID, sid, "access", time.Now().Add(time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(NewAIService(database, nil).AccountEvents())
	defer server.Close()
	req, _ := http.NewRequest(http.MethodGet, server.URL, nil)
	req.AddCookie(&http.Cookie{Name: TestingSessionCookieName, Value: access})
	res, err := http.DefaultClient.Do(req)
	if err != nil || res.StatusCode != http.StatusOK {
		t.Fatal("open stream", res, err)
	}
	defer res.Body.Close()
	lines := make(chan string, 16)
	go func() {
		defer close(lines)
		scanner := bufio.NewScanner(res.Body)
		for scanner.Scan() {
			lines <- scanner.Text()
		}
	}()
	select {
	case line := <-lines:
		if !strings.Contains(line, `"reset"`) {
			t.Fatal("stream did not start with reset", line)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("no initial reset")
	}
	// Several revalidations pass; the stream neither ends nor resets.
	deadline := time.After(500 * time.Millisecond)
wait:
	for {
		select {
		case line, open := <-lines:
			if !open {
				t.Fatal("active session's stream ended")
			}
			if strings.Contains(line, "reset") {
				t.Fatal("active session's stream reset", line)
			}
		case <-deadline:
			break wait
		}
	}
	if err := database.DeleteSession(security.HashToken(sid)); err != nil {
		t.Fatal(err)
	}
	timeout := time.After(5 * time.Second)
	for {
		select {
		case _, open := <-lines:
			if !open {
				return
			}
		case <-timeout:
			t.Fatal("revoked session's stream stayed open")
		}
	}
}
