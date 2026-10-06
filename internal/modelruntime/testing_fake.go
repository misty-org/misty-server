package modelruntime

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
)

// TestingCall is one model call a TestingFake received.
type TestingCall struct {
	Path string
	Body map[string]any
}

// TestingFake is an in-process agent runtime for tests. It rejects unsigned
// calls, records each call and answers with respond's status and JSON body.
type TestingFake struct {
	Server *httptest.Server
	Client *Client
	mu     sync.Mutex
	calls  []TestingCall
}

type testingCleanup interface {
	Helper()
	Cleanup(func())
}

func TestingNewFake(t testingCleanup, respond func(call TestingCall) (int, any)) *TestingFake {
	t.Helper()
	secret := []byte(strings.Repeat("m", 32))
	fake := &TestingFake{}
	fake.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		if r.Header.Get("X-Misty-Agent-Signature") != Sign(secret, r.Method, r.URL.Path, r.Header.Get("X-Misty-Agent-Timestamp"), raw) {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		call := TestingCall{Path: r.URL.Path}
		_ = json.Unmarshal(raw, &call.Body)
		fake.mu.Lock()
		fake.calls = append(fake.calls, call)
		fake.mu.Unlock()
		status, body := respond(call)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		_ = json.NewEncoder(w).Encode(body)
	}))
	t.Cleanup(fake.Server.Close)
	fake.Client = New(fake.Server.URL, secret, fake.Server.Client())
	return fake
}

// Calls returns the calls received so far.
func (f *TestingFake) Calls() []TestingCall {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]TestingCall(nil), f.calls...)
}
