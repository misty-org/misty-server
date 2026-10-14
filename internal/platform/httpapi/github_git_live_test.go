package api

import (
	"encoding/json"
	"net"
	"net/http"
	"os"
	"testing"
	"time"

	"github.com/kannachi323/misty/server/internal/integrations/composio"
)

// TestGitHubGitLiveBridge serves the GitHub Git steps for one connected
// account on loopback, so the desktop's transfer code can be tested end to end
// against a real (scratch) repository:
//
//	MISTY_GITHUB_LIVE_ACCOUNT=ca_... MISTY_GITHUB_LIVE_PORT=47311 go test ./internal/platform/httpapi -run TestGitHubGitLiveBridge -timeout 20m
//
// It stops on POST /stop or after 15 minutes.
func TestGitHubGitLiveBridge(t *testing.T) {
	account, port := os.Getenv("MISTY_GITHUB_LIVE_ACCOUNT"), os.Getenv("MISTY_GITHUB_LIVE_PORT")
	if account == "" || port == "" {
		t.Skip("set MISTY_GITHUB_LIVE_ACCOUNT and MISTY_GITHUB_LIVE_PORT for the live bridge")
	}
	client, err := composio.New(composio.ConfigFromEnv())
	if err != nil {
		t.Fatal(err)
	}
	listener, err := net.Listen("tcp", "127.0.0.1:"+port)
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan struct{})
	server := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/stop" {
			close(done)
			return
		}
		var request struct {
			Op   string          `json:"op"`
			Args json.RawMessage `json:"args"`
		}
		if json.NewDecoder(r.Body).Decode(&request) != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"message": "malformed"})
			return
		}
		call := func(method, endpoint string, body any) (composio.ProxyResult, error) {
			return client.Proxy(r.Context(), account, method, endpoint, body)
		}
		out, err := githubOperation(call, request.Op, request.Args)
		if err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"message": err.Error()})
			return
		}
		writeJSON(w, http.StatusOK, out)
	})}
	go func() { _ = server.Serve(listener) }()
	select {
	case <-done:
	case <-time.After(15 * time.Minute):
	}
	_ = server.Close()
}
