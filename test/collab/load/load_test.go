package load

import (
	"bufio"
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http/httptest"
	"os"
	"os/exec"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gorilla/websocket"

	"github.com/misty-org/misty-server/internal/app"
	"github.com/misty-org/misty-server/internal/collab/wire"
	"github.com/misty-org/misty-server/internal/collab/ydoc"
	"github.com/misty-org/misty-server/internal/platform/db"
	"github.com/misty-org/misty-server/internal/platform/security"
	"github.com/misty-org/misty-server/test/testkit"
)

const gigabyte = 1 << 30

type room struct {
	Key   string `json:"key"`
	Bytes int    `json:"bytes"`
	Conns int    `json:"conns"`
}

type plan struct {
	URL   string `json:"url"`
	Token string `json:"token"`
	Space string `json:"space"`
	Rooms []room `json:"rooms"`
}

func setting(name string, fallback int) int {
	if value, err := strconv.Atoi(strings.TrimSpace(os.Getenv(name))); err == nil && value > 0 {
		return value
	}
	return fallback
}

type instance struct {
	server  *httptest.Server
	service *app.CollabService
	url     string
}

func startInstance(t *testing.T, database *db.Database) *instance {
	t.Helper()
	server := httptest.NewUnstartedServer(nil)
	url := "http://" + server.Listener.Addr().String()
	t.Setenv("MISTY_COLLAB_SELF_URL", url)
	t.Setenv("MISTY_COLLAB_URL", url)
	service, err := app.NewCollabService(database)
	if err != nil {
		t.Fatal(err)
	}
	server.Config.Handler = service.Router
	server.Start()
	return &instance{server: server, service: service, url: url}
}

func (i *instance) stop() {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	i.service.Rooms.Shutdown(ctx)
	i.server.Close()
	i.service.Store.Close()
}

func heapInUse() uint64 {
	runtime.GC()
	runtime.GC()
	var stats runtime.MemStats
	runtime.ReadMemStats(&stats)
	return stats.HeapInuse
}

func TestCollabLoad(t *testing.T) {
	if os.Getenv("MISTY_COLLAB_LOAD") != "1" {
		t.Skip("set MISTY_COLLAB_LOAD=1 to run the room load test")
	}
	key := make([]byte, 32)
	_, _ = rand.Read(key)
	t.Setenv("MISTY_AUTH_SIGNING_KEY", base64.StdEncoding.EncodeToString(key))
	t.Setenv("MISTY_ENVIRONMENT", "")
	for _, name := range []string{"R2_ENDPOINT", "R2_BUCKET", "R2_ACCESS_KEY", "R2_SECRET_KEY"} {
		t.Setenv(name, "")
	}
	database := testkit.OpenDatabase(t)
	ctx := context.Background()
	user, err := database.CreateUserWithUsername("loadtester", "load@collab.test", "collab-load-"+base64.RawURLEncoding.EncodeToString(key[:6]))
	if err != nil {
		t.Fatal(err)
	}
	space, err := database.TestingCreateSpace(ctx, user.ID, "Load")
	if err != nil {
		t.Fatal(err)
	}
	signer, err := security.SessionSignerFromEnv()
	if err != nil {
		t.Fatal(err)
	}
	token, err := signer.Mint(user.ID, "load-session", "access", time.Now().Add(time.Hour))
	if err != nil {
		t.Fatal(err)
	}

	typicalRooms := setting("MISTY_COLLAB_LOAD_ROOMS", 100)
	typicalConns := setting("MISTY_COLLAB_LOAD_CONNECTIONS", 400)
	largeRooms := setting("MISTY_COLLAB_LOAD_LARGE_ROOMS", 4)

	one := startInstance(t, database)
	defer one.stop()
	scenario := func(label string, rooms, bytes, conns int) {
		base := heapInUse()
		first := plan{URL: one.url, Token: token, Space: space.ID}
		for index := 0; index < rooms; index++ {
			first.Rooms = append(first.Rooms, room{Key: fmt.Sprintf("%s-%d", label, index), Bytes: bytes, Conns: 1})
		}
		roomsHeld := spawnClients(t, first)
		defer roomsHeld()
		time.Sleep(3 * time.Second)
		withRooms := heapInUse()
		extra := plan{URL: one.url, Token: token, Space: space.ID}
		for index := 0; index < conns; index++ {
			extra.Rooms = append(extra.Rooms, room{Key: fmt.Sprintf("%s-%d", label, index%rooms), Conns: 1})
		}
		connsHeld := spawnClients(t, extra)
		defer connsHeld()
		withConns := heapInUse()
		perRoom := float64(int64(withRooms)-int64(base)) / float64(rooms)
		perConn := float64(int64(withConns)-int64(withRooms)) / float64(conns)
		t.Logf("%s: %d rooms of %d KiB: heap in use %.1f MiB, %.1f KiB a room with its first connection, %.0f rooms per GB",
			label, rooms, bytes/1024, float64(int64(withRooms)-int64(base))/(1<<20), perRoom/1024, gigabyte/perRoom)
		t.Logf("%s: %d more connections: heap in use %.1f MiB, %.1f KiB a connection, %.0f connections per GB",
			label, conns, float64(int64(withConns)-int64(withRooms))/(1<<20), perConn/1024, gigabyte/perConn)
	}
	scenario("typical", typicalRooms, 100*1024, typicalConns)
	scenario("large", largeRooms, 8*1024*1024-64*1024, largeRooms*4)

	two := startInstance(t, database)
	defer two.stop()
	passRooms(t, one, two, token, space.ID)
}

func TestCollabLoadClients(t *testing.T) {
	raw := os.Getenv("MISTY_COLLAB_LOAD_CLIENT")
	if raw == "" {
		t.Skip("a helper the load test starts")
	}
	var p plan
	if err := json.Unmarshal([]byte(raw), &p); err != nil {
		t.Fatal(err)
	}
	var mu sync.Mutex
	var conns []*websocket.Conn
	var wait sync.WaitGroup
	failures := make(chan error, len(p.Rooms)*4)
	limit := make(chan struct{}, 32)
	for _, r := range p.Rooms {
		for index := 0; index < r.Conns; index++ {
			wait.Add(1)
			limit <- struct{}{}
			go func(r room, first bool) {
				defer wait.Done()
				defer func() { <-limit }()
				conn, err := openRoom(p.URL, p.Token, p.Space, r.Key)
				if err != nil {
					failures <- err
					return
				}
				mu.Lock()
				conns = append(conns, conn)
				mu.Unlock()
				if first && r.Bytes > 0 {
					if err := fill(conn, r.Bytes); err != nil {
						failures <- err
					}
				}
			}(r, index == 0)
		}
	}
	wait.Wait()
	close(failures)
	for err := range failures {
		t.Fatal(err)
	}
	fmt.Println("ready", len(conns))
	_, _ = io.Copy(io.Discard, os.Stdin)
	for _, conn := range conns {
		_ = conn.Close()
	}
}

func spawnClients(t *testing.T, p plan) func() {
	t.Helper()
	raw, err := json.Marshal(p)
	if err != nil {
		t.Fatal(err)
	}
	command := exec.Command(os.Args[0], "-test.run=^TestCollabLoadClients$", "-test.v", "-test.timeout=30m")
	command.Env = append(os.Environ(), "MISTY_COLLAB_LOAD_CLIENT="+string(raw), "MISTY_COLLAB_LOAD=")
	stdin, err := command.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	stdout, err := command.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	command.Stderr = os.Stderr
	if err := command.Start(); err != nil {
		t.Fatal(err)
	}
	ready := make(chan error, 1)
	go func() {
		scanner := bufio.NewScanner(stdout)
		for scanner.Scan() {
			if strings.HasPrefix(scanner.Text(), "ready ") {
				ready <- nil
				_, _ = io.Copy(io.Discard, stdout)
				return
			}
		}
		ready <- errors.New("the load clients stopped before they were ready")
	}()
	select {
	case err := <-ready:
		if err != nil {
			_ = command.Process.Kill()
			t.Fatal(err)
		}
	case <-time.After(10 * time.Minute):
		_ = command.Process.Kill()
		t.Fatal("the load clients took too long")
	}
	return func() {
		_ = stdin.Close()
		done := make(chan struct{})
		go func() {
			_ = command.Wait()
			close(done)
		}()
		select {
		case <-done:
		case <-time.After(30 * time.Second):
			_ = command.Process.Kill()
			<-done
		}
	}
}

func socketURL(url string) string {
	return "ws" + strings.TrimPrefix(url, "http") + "/v1/collab"
}

type refusal struct {
	code  int
	moved string
}

func (r *refusal) Error() string { return fmt.Sprintf("closed with %d (moved to %q)", r.code, r.moved) }

func openRoom(url, token, space, key string) (*websocket.Conn, error) {
	dialer := websocket.Dialer{ReadBufferSize: 1024, WriteBufferSize: 1024, HandshakeTimeout: 30 * time.Second}
	conn, _, err := dialer.Dial(socketURL(url), nil)
	if err != nil {
		return nil, err
	}
	open, _ := json.Marshal(map[string]any{"type": "open", "v": 1, "space": space, "key": key, "options": map[string]any{},
		"token": token, "client": "collab-load-test"})
	if err := conn.WriteMessage(websocket.TextMessage, open); err != nil {
		_ = conn.Close()
		return nil, err
	}
	_ = conn.SetReadDeadline(time.Now().Add(60 * time.Second))
	moved := ""
	for {
		kind, data, err := conn.ReadMessage()
		if err != nil {
			_ = conn.Close()
			var closed *websocket.CloseError
			if errors.As(err, &closed) {
				return nil, &refusal{code: closed.Code, moved: moved}
			}
			return nil, err
		}
		if kind != websocket.TextMessage {
			continue
		}
		var message struct {
			Type string `json:"type"`
			URL  string `json:"url"`
		}
		_ = json.Unmarshal(data, &message)
		if message.Type == "moved" {
			moved = message.URL
		}
		if message.Type == "opened" {
			_ = conn.SetReadDeadline(time.Time{})
			return conn, nil
		}
	}
}

func fill(conn *websocket.Conn, bytes int) error {
	doc := ydoc.New(true)
	defer doc.Destroy()
	if err := doc.Change(nil, func(tx *ydoc.Tx) { tx.Text("content").Insert(tx, 0, strings.Repeat("x", bytes), nil) }); err != nil {
		return err
	}
	update, err := doc.Update()
	if err != nil {
		return err
	}
	if err := conn.WriteMessage(websocket.BinaryMessage, wire.UpdateFrame(update).Data); err != nil {
		return err
	}
	_, err = held(conn)
	return err
}

func held(conn *websocket.Conn) (int, error) {
	if err := conn.WriteMessage(websocket.BinaryMessage, wire.SyncStep1Frame([]byte{0}).Data); err != nil {
		return 0, err
	}
	_ = conn.SetReadDeadline(time.Now().Add(60 * time.Second))
	defer func() { _ = conn.SetReadDeadline(time.Time{}) }()
	for {
		kind, data, err := conn.ReadMessage()
		if err != nil {
			return 0, err
		}
		if kind != websocket.BinaryMessage {
			continue
		}
		message, payload, err := wire.Kind(data)
		if err != nil || message != wire.MessageSync {
			continue
		}
		step, update, err := wire.Sync(payload)
		if err != nil || step != wire.SyncStep2 {
			continue
		}
		doc, err := ydoc.Load(update, true)
		if err != nil {
			return 0, err
		}
		defer doc.Destroy()
		text, err := doc.Text("content")
		if err != nil {
			return 0, err
		}
		return text.Len(), nil
	}
}

func passRooms(t *testing.T, one, two *instance, token, space string) {
	t.Helper()
	owner, err := openRoom(one.url, token, space, "passed")
	if err != nil {
		t.Fatal(err)
	}
	if err := fill(owner, 64*1024); err != nil {
		t.Fatal(err)
	}
	_, err = openRoom(two.url, token, space, "passed")
	var refused *refusal
	if !errors.As(err, &refused) || refused.code != wire.CloseMoved || refused.moved != one.url {
		t.Fatalf("the second instance answered %v, want moved to %s", err, one.url)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	handedBack := make(chan struct{})
	go func() {
		one.service.Rooms.Shutdown(ctx)
		close(handedBack)
	}()
	_ = owner.SetReadDeadline(time.Now().Add(30 * time.Second))
	for {
		if _, _, err := owner.ReadMessage(); err != nil {
			var closed *websocket.CloseError
			if !errors.As(err, &closed) || closed.Code != wire.CloseMoved {
				t.Fatalf("the owner's connection ended with %v, want moved", err)
			}
			break
		}
	}
	_ = owner.Close()
	select {
	case <-handedBack:
	case <-ctx.Done():
		t.Fatal("the first instance didn't hand its rooms back")
	}
	taken, err := openRoom(two.url, token, space, "passed")
	if err != nil {
		t.Fatalf("the second instance didn't take the room: %v", err)
	}
	defer taken.Close()
	length, err := held(taken)
	if err != nil || length != 64*1024 {
		t.Fatalf("the room passed with %d characters, %v", length, err)
	}
	t.Logf("two instances: the room was refused as moved to its owner, then handed back on shutdown and taken by the other with its %d KiB", length/1024)
}
