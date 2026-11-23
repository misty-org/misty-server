package api

import (
	"bytes"
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/misty-org/misty-server/internal/platform/config"
)

const (
	liveKitAloneLimit    = 5 * time.Minute
	liveKitSweepInterval = time.Minute
	liveKitDeleteRetry   = 15 * time.Second
	callRemovedAttribute = "misty.removed"
)

type LiveKitRoomWatcher struct {
	baseURL, key, secret string
	client               *http.Client
	now                  func() time.Time
	after                func(d time.Duration, f func()) (stop func())
	limit                time.Duration

	mu    sync.Mutex
	ctx   context.Context
	rooms map[string]*liveKitRoomState
}

type liveKitRoomState struct {
	sid          string
	people, gone map[string]bool
	stop         func()
	gen          int
	heard        time.Time
	mismatches   int
}

func NewLiveKitRoomWatcher() *LiveKitRoomWatcher {
	publicURL := strings.TrimSpace(config.Getenv("LIVEKIT_URL"))
	key := strings.TrimSpace(config.Getenv("LIVEKIT_API_KEY"))
	secret := strings.TrimSpace(config.Getenv("LIVEKIT_API_SECRET"))
	if publicURL == "" || key == "" || secret == "" {
		return nil
	}
	base := strings.TrimSpace(config.Getenv("LIVEKIT_ADMIN_URL"))
	if base == "" {
		base = liveKitAdminURL(publicURL)
	}
	return newLiveKitRoomWatcher(base, key, secret)
}

func newLiveKitRoomWatcher(baseURL, key, secret string) *LiveKitRoomWatcher {
	return &LiveKitRoomWatcher{
		baseURL: strings.TrimRight(baseURL, "/"),
		key:     key,
		secret:  secret,
		client:  &http.Client{Timeout: 10 * time.Second},
		now:     time.Now,
		after: func(d time.Duration, f func()) func() {
			timer := time.AfterFunc(d, f)
			return func() { timer.Stop() }
		},
		limit: liveKitAloneLimit,
		rooms: map[string]*liveKitRoomState{},
	}
}

func liveKitAdminURL(publicURL string) string {
	switch {
	case strings.HasPrefix(publicURL, "wss://"):
		return "https://" + strings.TrimPrefix(publicURL, "wss://")
	case strings.HasPrefix(publicURL, "ws://"):
		return "http://" + strings.TrimPrefix(publicURL, "ws://")
	}
	return publicURL
}

func (w *LiveKitRoomWatcher) Run(ctx context.Context) {
	if w == nil {
		return
	}
	w.mu.Lock()
	w.ctx = ctx
	w.mu.Unlock()
	defer w.stopAll()
	ticker := time.NewTicker(liveKitSweepInterval)
	defer ticker.Stop()
	for {
		sweep, cancel := context.WithTimeout(ctx, 30*time.Second)
		if err := w.Sweep(sweep); err != nil && ctx.Err() == nil {
			log.Printf("livekit lone rooms: %v", err)
		}
		cancel()
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

func (w *LiveKitRoomWatcher) stopAll() {
	w.mu.Lock()
	defer w.mu.Unlock()
	for name := range w.rooms {
		w.forget(name)
	}
}

type liveKitRoom struct {
	SID                  string `json:"sid"`
	Name                 string `json:"name"`
	NumParticipants      uint32 `json:"num_participants"`
	NumParticipantsCamel uint32 `json:"numParticipants"`
}

func (r liveKitRoom) people() int {
	return int(max(r.NumParticipants, r.NumParticipantsCamel))
}

type liveKitParticipant struct {
	SID      string `json:"sid"`
	Identity string `json:"identity"`
}

type liveKitEvent struct {
	Event       string              `json:"event"`
	Room        *liveKitRoom        `json:"room"`
	Participant *liveKitParticipant `json:"participant"`
}

func (w *LiveKitRoomWatcher) Webhook() http.HandlerFunc {
	return func(rw http.ResponseWriter, r *http.Request) {
		if w == nil {
			http.NotFound(rw, r)
			return
		}
		body, err := io.ReadAll(io.LimitReader(r.Body, 1<<20))
		if err != nil {
			writeJSON(rw, http.StatusBadRequest, map[string]string{"code": "invalid_request"})
			return
		}
		if !w.signedByLiveKit(r.Header.Get("Authorization"), body) {
			writeJSON(rw, http.StatusUnauthorized, map[string]string{"code": "unauthorized"})
			return
		}
		var event liveKitEvent
		if err := json.Unmarshal(body, &event); err != nil {
			writeJSON(rw, http.StatusBadRequest, map[string]string{"code": "invalid_request"})
			return
		}
		w.handle(event)
		if event.Event == "participant_joined" {
			w.checkJoined(event)
		}
		rw.WriteHeader(http.StatusOK)
	}
}

func (w *LiveKitRoomWatcher) signedByLiveKit(header string, body []byte) bool {
	raw := strings.TrimSpace(strings.TrimPrefix(header, "Bearer "))
	if raw == "" {
		return false
	}
	claims := jwt.MapClaims{}
	if _, err := jwt.ParseWithClaims(raw, claims, func(*jwt.Token) (any, error) { return []byte(w.secret), nil },
		jwt.WithValidMethods([]string{"HS256"}), jwt.WithIssuer(w.key), jwt.WithLeeway(30*time.Second)); err != nil {
		return false
	}
	got, _ := claims["sha256"].(string)
	sum := sha256.Sum256(body)
	want := base64.StdEncoding.EncodeToString(sum[:])
	return subtle.ConstantTimeCompare([]byte(got), []byte(want)) == 1
}

func (w *LiveKitRoomWatcher) handle(event liveKitEvent) {
	if event.Room == nil || event.Room.Name == "" {
		return
	}
	name, sid := event.Room.Name, event.Room.SID
	w.mu.Lock()
	defer w.mu.Unlock()
	if event.Event == "room_finished" {
		if st := w.rooms[name]; st != nil && (st.sid == "" || sid == "" || st.sid == sid) {
			w.forget(name)
		}
		return
	}
	var person string
	if event.Participant != nil {
		person = event.Participant.SID
	}
	switch event.Event {
	case "room_started":
		w.room(name, sid)
	case "participant_joined":
		st := w.room(name, sid)
		if person != "" && !st.gone[person] {
			st.people[person] = true
		}
	case "participant_left", "participant_connection_aborted":
		st := w.room(name, sid)
		if person != "" {
			delete(st.people, person)
			st.gone[person] = true
		}
	default:
		return
	}
	st := w.rooms[name]
	st.heard = w.now()
	st.mismatches = 0
	w.settle(name, st)
}

func (w *LiveKitRoomWatcher) room(name, sid string) *liveKitRoomState {
	st := w.rooms[name]
	if st != nil && sid != "" && st.sid != "" && st.sid != sid {
		w.forget(name)
		st = nil
	}
	if st == nil {
		st = &liveKitRoomState{people: map[string]bool{}, gone: map[string]bool{}, heard: w.now()}
		w.rooms[name] = st
	}
	if st.sid == "" {
		st.sid = sid
	}
	return st
}

func (w *LiveKitRoomWatcher) settle(name string, st *liveKitRoomState) {
	if len(st.people) >= 2 {
		if st.stop != nil {
			st.stop()
			st.stop = nil
			st.gen++
		}
		return
	}
	if st.stop == nil {
		w.arm(name, st, w.limit)
	}
}

func (w *LiveKitRoomWatcher) arm(name string, st *liveKitRoomState, d time.Duration) {
	st.gen++
	gen := st.gen
	st.stop = w.after(d, func() { w.expire(name, gen) })
}

func (w *LiveKitRoomWatcher) forget(name string) {
	if st := w.rooms[name]; st != nil {
		if st.stop != nil {
			st.stop()
		}
		st.gen++
		delete(w.rooms, name)
	}
}

func (w *LiveKitRoomWatcher) expire(name string, gen int) {
	w.mu.Lock()
	st := w.rooms[name]
	if st == nil || st.gen != gen || st.stop == nil || len(st.people) >= 2 {
		w.mu.Unlock()
		return
	}
	st.stop = nil
	ctx := w.ctx
	w.mu.Unlock()
	if ctx == nil {
		ctx = context.Background()
	}
	call, cancel := context.WithTimeout(ctx, 10*time.Second)
	err := w.call(call, "DeleteRoom", name, map[string]any{"room": name}, nil)
	cancel()
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.rooms[name] != st || st.gen != gen {
		return
	}
	if err != nil {
		if ctx.Err() == nil {
			log.Printf("livekit lone rooms: delete room %q: %v", name, err)
			w.arm(name, st, liveKitDeleteRetry)
		}
		return
	}
	w.forget(name)
}

func (w *LiveKitRoomWatcher) Sweep(ctx context.Context) error {
	var listed struct {
		Rooms []liveKitRoom `json:"rooms"`
	}
	if err := w.call(ctx, "ListRooms", "", map[string]any{}, &listed); err != nil {
		return fmt.Errorf("list rooms: %w", err)
	}
	now := w.now()
	reread := []liveKitRoom{}
	w.mu.Lock()
	seen := make(map[string]bool, len(listed.Rooms))
	for _, room := range listed.Rooms {
		seen[room.Name] = true
		st := w.rooms[room.Name]
		switch {
		case st == nil || (room.SID != "" && st.sid != "" && st.sid != room.SID):
			reread = append(reread, room)
		case len(st.people) != room.people():
			st.mismatches++
			if st.mismatches >= 2 {
				reread = append(reread, room)
			}
		default:
			st.mismatches = 0
		}
	}
	for name, st := range w.rooms {
		if !seen[name] && now.Sub(st.heard) >= liveKitSweepInterval {
			w.forget(name)
		}
	}
	w.mu.Unlock()
	var failed error
	for _, room := range reread {
		var participants struct {
			Participants []liveKitParticipant `json:"participants"`
		}
		if err := w.call(ctx, "ListParticipants", room.Name, map[string]any{"room": room.Name}, &participants); err != nil {
			failed = fmt.Errorf("list participants of %q: %w", room.Name, err)
			continue
		}
		w.mu.Lock()
		st := w.room(room.Name, room.SID)
		st.people = map[string]bool{}
		for _, person := range participants.Participants {
			if person.SID != "" && !st.gone[person.SID] {
				st.people[person.SID] = true
			}
		}
		st.heard = now
		st.mismatches = 0
		w.settle(room.Name, st)
		w.mu.Unlock()
	}
	return failed
}

func (w *LiveKitRoomWatcher) call(ctx context.Context, method, room string, body any, out any) error {
	token, err := w.adminToken(room)
	if err != nil {
		return err
	}
	payload, err := json.Marshal(body)
	if err != nil {
		return err
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost,
		w.baseURL+"/twirp/livekit.RoomService/"+method, bytes.NewReader(payload))
	if err != nil {
		return err
	}
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Authorization", "Bearer "+token)
	response, err := w.client.Do(request)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	data, err := io.ReadAll(io.LimitReader(response.Body, 4<<20))
	if err != nil {
		return err
	}
	if response.StatusCode != http.StatusOK {
		return fmt.Errorf("livekit answered %d: %s", response.StatusCode, strings.TrimSpace(string(data)))
	}
	if out == nil {
		return nil
	}
	return json.Unmarshal(data, out)
}

func (w *LiveKitRoomWatcher) adminToken(room string) (string, error) {
	now := time.Now()
	video := map[string]any{"roomList": true, "roomAdmin": true, "roomCreate": true}
	if room != "" {
		video["room"] = room
	}
	claims := jwt.MapClaims{
		"iss":   w.key,
		"nbf":   now.Add(-10 * time.Second).Unix(),
		"exp":   now.Add(time.Minute).Unix(),
		"video": video,
	}
	return jwt.NewWithClaims(jwt.SigningMethodHS256, claims).SignedString([]byte(w.secret))
}
