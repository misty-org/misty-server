package api

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

type fakeLiveKit struct {
	secret string

	mu          sync.Mutex
	rooms       map[string][]string
	deleted     []string
	failDeletes bool
}

func (f *fakeLiveKit) setRoom(name string, people ...string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.rooms == nil {
		f.rooms = map[string][]string{}
	}
	f.rooms[name] = people
}

func (f *fakeLiveKit) takeDeleted() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := f.deleted
	f.deleted = nil
	return out
}

func (f *fakeLiveKit) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost || r.Header.Get("Content-Type") != "application/json" {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}
	raw := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
	claims := jwt.MapClaims{}
	if _, err := jwt.ParseWithClaims(raw, claims, func(*jwt.Token) (any, error) { return []byte(f.secret), nil },
		jwt.WithValidMethods([]string{"HS256"})); err != nil || claims["iss"] != "misty" {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	video, _ := claims["video"].(map[string]any)
	var body struct {
		Room string `json:"room"`
	}
	_ = json.NewDecoder(r.Body).Decode(&body)
	f.mu.Lock()
	defer f.mu.Unlock()
	switch r.URL.Path {
	case "/twirp/livekit.RoomService/ListRooms":
		if video["roomList"] != true {
			http.Error(w, "permission denied", http.StatusUnauthorized)
			return
		}
		rooms := []map[string]any{}
		for name, people := range f.rooms {
			rooms = append(rooms, map[string]any{"sid": "RM_" + name, "name": name, "num_participants": len(people)})
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"rooms": rooms})
	case "/twirp/livekit.RoomService/ListParticipants":
		if video["roomAdmin"] != true || video["room"] != body.Room {
			http.Error(w, "permission denied", http.StatusUnauthorized)
			return
		}
		participants := []map[string]any{}
		for _, sid := range f.rooms[body.Room] {
			participants = append(participants, map[string]any{"sid": sid, "identity": "user-" + sid})
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"participants": participants})
	case "/twirp/livekit.RoomService/DeleteRoom":
		if video["roomCreate"] != true || body.Room == "" {
			http.Error(w, "permission denied", http.StatusUnauthorized)
			return
		}
		if f.failDeletes {
			http.Error(w, "unavailable", http.StatusServiceUnavailable)
			return
		}
		f.deleted = append(f.deleted, body.Room)
		delete(f.rooms, body.Room)
		_, _ = w.Write([]byte("{}"))
	default:
		http.NotFound(w, r)
	}
}

type fakeTimers struct {
	mu      sync.Mutex
	pending []*fakeTimer
}

type fakeTimer struct {
	d       time.Duration
	f       func()
	stopped bool
}

func (t *fakeTimers) after(d time.Duration, f func()) func() {
	t.mu.Lock()
	defer t.mu.Unlock()
	timer := &fakeTimer{d: d, f: f}
	t.pending = append(t.pending, timer)
	return func() {
		t.mu.Lock()
		defer t.mu.Unlock()
		timer.stopped = true
	}
}

func (t *fakeTimers) running() []time.Duration {
	t.mu.Lock()
	defer t.mu.Unlock()
	out := []time.Duration{}
	for _, timer := range t.pending {
		if !timer.stopped {
			out = append(out, timer.d)
		}
	}
	return out
}

func (t *fakeTimers) fire() {
	t.mu.Lock()
	due := []*fakeTimer{}
	for _, timer := range t.pending {
		if !timer.stopped {
			timer.stopped = true
			due = append(due, timer)
		}
	}
	t.mu.Unlock()
	for _, timer := range due {
		timer.f()
	}
}

type liveKitHarness struct {
	t       *testing.T
	fake    *fakeLiveKit
	timers  *fakeTimers
	watcher *LiveKitRoomWatcher
	clock   time.Time
}

func newLiveKitHarness(t *testing.T) *liveKitHarness {
	t.Helper()
	fake := &fakeLiveKit{secret: "secret"}
	server := httptest.NewServer(fake)
	t.Cleanup(server.Close)
	h := &liveKitHarness{t: t, fake: fake, timers: &fakeTimers{},
		clock: time.Date(2026, 11, 21, 12, 0, 0, 0, time.UTC)}
	h.watcher = newLiveKitRoomWatcher(server.URL, "misty", "secret")
	h.watcher.after = h.timers.after
	h.watcher.now = func() time.Time { return h.clock }
	return h
}

func (h *liveKitHarness) send(event, room, participant string) int {
	h.t.Helper()
	payload := map[string]any{"event": event, "room": map[string]any{"sid": "RM_" + room, "name": room}}
	if participant != "" {
		payload["participant"] = map[string]any{"sid": participant, "identity": "user-" + participant}
	}
	body, _ := json.Marshal(payload)
	return h.sendRaw(body, h.sign(body, "secret"))
}

func (h *liveKitHarness) sign(body []byte, secret string) string {
	sum := sha256.Sum256(body)
	token, err := jwt.NewWithClaims(jwt.SigningMethodHS256, jwt.MapClaims{
		"iss":    "misty",
		"exp":    time.Now().Add(5 * time.Minute).Unix(),
		"sha256": base64.StdEncoding.EncodeToString(sum[:]),
	}).SignedString([]byte(secret))
	if err != nil {
		h.t.Fatal(err)
	}
	return token
}

func (h *liveKitHarness) sendRaw(body []byte, token string) int {
	request := httptest.NewRequest(http.MethodPost, "/v1/calls/livekit-events", strings.NewReader(string(body)))
	request.Header.Set("Content-Type", "application/webhook+json")
	request.Header.Set("Authorization", token)
	recorder := httptest.NewRecorder()
	h.watcher.Webhook()(recorder, request)
	return recorder.Code
}

func (h *liveKitHarness) sweep() {
	h.t.Helper()
	if err := h.watcher.Sweep(context.Background()); err != nil {
		h.t.Fatalf("sweep: %v", err)
	}
}

func (h *liveKitHarness) wantTimers(want ...time.Duration) {
	h.t.Helper()
	got := h.timers.running()
	if len(got) != len(want) {
		h.t.Fatalf("running timers %v, want %v", got, want)
	}
	for i := range got {
		if got[i] != want[i] {
			h.t.Fatalf("running timers %v, want %v", got, want)
		}
	}
}

func TestLiveKitWebhookRunsTheAloneTimerFromJoinsAndLeaves(t *testing.T) {
	h := newLiveKitHarness(t)
	h.fake.setRoom("dm:1", "PA")

	h.send("room_started", "dm:1", "")
	h.send("participant_joined", "dm:1", "PA")
	h.wantTimers(5 * time.Minute)

	h.send("participant_joined", "dm:1", "PB")
	h.wantTimers()

	h.send("participant_left", "dm:1", "PB")
	h.wantTimers(5 * time.Minute)

	h.timers.fire()
	if got := h.fake.takeDeleted(); len(got) != 1 || got[0] != "dm:1" {
		t.Fatalf("deleted %v, want [dm:1]", got)
	}
	h.wantTimers()
	if len(h.watcher.rooms) != 0 {
		t.Fatalf("kept rooms after deleting: %v", h.watcher.rooms)
	}
}

func TestLiveKitWebhookRefusesUnsignedEvents(t *testing.T) {
	h := newLiveKitHarness(t)
	body := []byte(`{"event":"participant_joined","room":{"sid":"RM_dm:1","name":"dm:1"},"participant":{"sid":"PA"}}`)

	if code := h.sendRaw(body, ""); code != http.StatusUnauthorized {
		t.Fatalf("no token: %d, want 401", code)
	}
	if code := h.sendRaw(body, h.sign(body, "other secret")); code != http.StatusUnauthorized {
		t.Fatalf("wrong secret: %d, want 401", code)
	}
	if code := h.sendRaw(body, h.sign([]byte(`{}`), "secret")); code != http.StatusUnauthorized {
		t.Fatalf("another body's hash: %d, want 401", code)
	}
	h.wantTimers()
	if code := h.sendRaw(body, h.sign(body, "secret")); code != http.StatusOK {
		t.Fatalf("signed: %d, want 200", code)
	}
	h.wantTimers(5 * time.Minute)
}

func TestLiveKitWebhookIgnoresAJoinArrivingAfterItsLeave(t *testing.T) {
	h := newLiveKitHarness(t)
	h.send("participant_joined", "dm:1", "PA")
	h.send("participant_left", "dm:1", "PB")
	h.send("participant_joined", "dm:1", "PB")
	h.wantTimers(5 * time.Minute)
}

func TestLiveKitWebhookRoomFinishedStopsTheTimer(t *testing.T) {
	h := newLiveKitHarness(t)
	h.send("participant_joined", "dm:1", "PA")
	h.send("room_finished", "dm:1", "")
	h.wantTimers()
	h.timers.fire()
	if got := h.fake.takeDeleted(); len(got) != 0 {
		t.Fatalf("deleted %v after the room finished", got)
	}
}

func TestLiveKitSweepPicksUpRoomsAfterARestart(t *testing.T) {
	h := newLiveKitHarness(t)
	h.fake.setRoom("dm:alone", "PA")
	h.fake.setRoom("space:s1:voice:general", "PB", "PC")

	h.sweep()
	h.wantTimers(5 * time.Minute)
	h.timers.fire()
	if got := h.fake.takeDeleted(); len(got) != 1 || got[0] != "dm:alone" {
		t.Fatalf("deleted %v, want [dm:alone]", got)
	}

	h.send("participant_left", "space:s1:voice:general", "PC")
	h.wantTimers(5 * time.Minute)
}

func TestLiveKitSweepCorrectsAMissedLeave(t *testing.T) {
	h := newLiveKitHarness(t)
	h.fake.setRoom("dm:1", "PA")
	h.send("participant_joined", "dm:1", "PA")
	h.send("participant_joined", "dm:1", "PB")
	h.sweep()
	h.wantTimers()
	h.sweep()
	h.wantTimers(5 * time.Minute)
}

func TestLiveKitSweepDropsRoomsLiveKitClosed(t *testing.T) {
	h := newLiveKitHarness(t)
	h.send("participant_joined", "dm:1", "PA")
	h.wantTimers(5 * time.Minute)

	h.sweep()
	h.wantTimers(5 * time.Minute)

	h.clock = h.clock.Add(time.Minute)
	h.sweep()
	h.wantTimers()
	if len(h.watcher.rooms) != 0 {
		t.Fatalf("kept rooms LiveKit closed: %v", h.watcher.rooms)
	}
}

func TestLiveKitDeleteFailureIsTriedAgain(t *testing.T) {
	h := newLiveKitHarness(t)
	h.fake.setRoom("dm:1", "PA")
	h.send("participant_joined", "dm:1", "PA")

	h.fake.failDeletes = true
	h.timers.fire()
	h.wantTimers(liveKitDeleteRetry)

	h.fake.failDeletes = false
	h.timers.fire()
	if got := h.fake.takeDeleted(); len(got) != 1 || got[0] != "dm:1" {
		t.Fatalf("deleted %v, want [dm:1]", got)
	}
}

func TestLiveKitAdminURL(t *testing.T) {
	for in, want := range map[string]string{
		"wss://calls.example.com": "https://calls.example.com",
		"ws://localhost:7880":     "http://localhost:7880",
		"https://calls.example":   "https://calls.example",
	} {
		if got := liveKitAdminURL(in); got != want {
			t.Errorf("liveKitAdminURL(%q) = %q, want %q", in, got, want)
		}
	}
}
