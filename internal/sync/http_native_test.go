package browsersync

import (
	"bytes"
	"context"
	"encoding/base64"
	"github.com/kannachi323/misty/server/internal/accounts"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/kannachi323/misty/server/internal/platform/security"
)

func TestBrowserSyncNativeWorkerAgainstGo(t *testing.T) {
	binary := os.Getenv("MISTY_BROWSER_SYNC_NATIVE_FIXTURE")
	if binary == "" {
		t.Skip("requires the built Rust live_protocol_fixture example")
	}
	database, peer := browserSocketTestDatabase(t)
	t.Setenv("MISTY_AUTH_SIGNING_KEY", base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{23}, 32)))
	signer, err := security.SessionSignerFromEnv()
	if err != nil {
		t.Fatal(err)
	}
	cookie, err := signer.Mint("owner", "fixture-session", "access", time.Now().Add(5*time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	one, two := NewBrowserSyncService(database), NewBrowserSyncService(peer)
	var offline atomic.Bool
	var connections atomic.Int32
	var ticketRequests atomic.Int32
	var droppedBootstrap atomic.Bool
	var mu sync.Mutex
	hijacked := map[net.Conn]struct{}{}
	closeSockets := func() {
		mu.Lock()
		defer mu.Unlock()
		for conn := range hijacked {
			_ = conn.Close()
			delete(hijacked, conn)
		}
	}
	mux := http.NewServeMux()
	// The native client refreshes once after a 401. This disposable fixture
	// issues no refresh cookie, so an anonymous client remains unauthenticated.
	mux.HandleFunc("POST /auth/refresh", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
	})
	mux.Handle("GET /sync/workspace", one.Workspace())
	mux.HandleFunc("POST /sync/workspace", func(w http.ResponseWriter, r *http.Request) {
		response := httptest.NewRecorder()
		one.Workspace()(response, r)
		if response.Code == http.StatusCreated && droppedBootstrap.CompareAndSwap(false, true) {
			// The transaction committed, but the native device never receives its
			// acknowledgment. Its next connection must recover the same identity.
			connection, _, err := w.(http.Hijacker).Hijack()
			if err != nil {
				t.Error(err)
				return
			}
			_ = connection.Close()
			return
		}
		for name, values := range response.Header() {
			w.Header()[name] = values
		}
		w.WriteHeader(response.Code)
		_, _ = w.Write(response.Body.Bytes())
	})
	mux.Handle("GET /sync/devices", one.Devices())
	mux.Handle("POST /sync/devices", one.Devices())
	mux.HandleFunc("POST /sync/ticket", func(w http.ResponseWriter, r *http.Request) {
		ticketRequests.Add(1)
		if offline.Load() {
			w.WriteHeader(503)
			return
		}
		one.Ticket()(w, r)
	})
	mux.HandleFunc("GET /sync/ws", func(w http.ResponseWriter, r *http.Request) {
		if offline.Load() {
			w.WriteHeader(503)
			return
		}
		if connections.Add(1)%2 == 0 {
			two.Connect()(w, r)
		} else {
			one.Connect()(w, r)
		}
	})
	mux.HandleFunc("POST /fixture/offline", func(w http.ResponseWriter, r *http.Request) {
		offline.Store(true)
		closeSockets()
		w.WriteHeader(204)
	})
	mux.HandleFunc("POST /fixture/online", func(w http.ResponseWriter, r *http.Request) {
		offline.Store(false)
		w.WriteHeader(204)
	})
	server := httptest.NewUnstartedServer(mux)
	server.Config.ConnState = func(conn net.Conn, state http.ConnState) {
		if state == http.StateHijacked {
			mu.Lock()
			hijacked[conn] = struct{}{}
			mu.Unlock()
		}
	}
	server.Start()
	defer server.Close()
	defer closeSockets()
	ctx, cancel := context.WithTimeout(context.Background(), 75*time.Second)
	defer cancel()
	command := exec.CommandContext(ctx, binary)
	command.Env = append(os.Environ(), "MISTY_SYNC_FIXTURE_BASE="+server.URL, "MISTY_SYNC_FIXTURE_COOKIE="+accounts.SessionCookieName+"="+cookie)
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("native fixture: %v\n%s", err, output)
	}
	if !strings.Contains(string(output), "native_protocol_fixture_ok") {
		t.Fatal("native fixture did not finish")
	}
	workspace, err := NewStore(database.Conn).BrowserSyncWorkspace(context.Background(), "owner")
	if err != nil || workspace == nil || workspace.HeadSequence != 9 {
		t.Fatalf("unexpected durable head: %v", err)
	}
	var count int
	var leaked bool
	if err := database.Conn.QueryRow(`SELECT count(*),bool_or(envelope::text LIKE '%native-private-cookie-fixture%') FROM browser_sync_events`).Scan(&count, &leaked); err != nil {
		t.Fatal(err)
	}
	if count != 9 || leaked {
		t.Fatal("duplicate events or plaintext entered the backend")
	}
	if !droppedBootstrap.Load() {
		t.Fatal("fixture did not interrupt committed bootstrap")
	}
	devices, err := NewStore(database.Conn).BrowserSyncDevices(context.Background(), "owner", workspace.WorkspaceID)
	if err != nil || len(devices) != 2 {
		t.Fatalf("enrollment allocated duplicate identities: %v", err)
	}
	if connections.Load() < 4 || ticketRequests.Load() < 4 {
		t.Fatal("workers did not reconnect with fresh tickets")
	}
}

// TestBrowserSyncNativeTreesAgainstGo runs two real native workers through
// publish, claim, displacement and take-back on the tree protocol.
func TestBrowserSyncNativeTreesAgainstGo(t *testing.T) {
	binary := os.Getenv("MISTY_BROWSER_SYNC_TREE_FIXTURE")
	if binary == "" {
		t.Skip("requires the built Rust live_tree_fixture example")
	}
	database, _ := browserSocketTestDatabase(t)
	t.Setenv("MISTY_AUTH_SIGNING_KEY", base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{23}, 32)))
	signer, err := security.SessionSignerFromEnv()
	if err != nil {
		t.Fatal(err)
	}
	cookie, err := signer.Mint("owner", "fixture-session", "access", time.Now().Add(5*time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	service := NewBrowserSyncService(database)
	mux := http.NewServeMux()
	mux.HandleFunc("POST /auth/refresh", func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusUnauthorized) })
	mux.Handle("GET /sync/workspace", service.Workspace())
	mux.Handle("POST /sync/workspace", service.Workspace())
	mux.Handle("GET /sync/devices", service.Devices())
	mux.Handle("POST /sync/devices", service.Devices())
	mux.Handle("POST /sync/control", service.ControlDevice())
	mux.Handle("POST /sync/ticket", service.Ticket())
	mux.Handle("GET /sync/ws", service.Connect())
	server := httptest.NewServer(mux)
	defer server.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	command := exec.CommandContext(ctx, binary)
	command.Env = append(os.Environ(), "MISTY_SYNC_FIXTURE_BASE="+server.URL, "MISTY_SYNC_FIXTURE_COOKIE="+accounts.SessionCookieName+"="+cookie)
	output, err := command.CombinedOutput()
	if err != nil || !strings.Contains(string(output), "native_tree_fixture_ok") {
		t.Fatalf("native tree fixture: %v\n%s", err, output)
	}
}
