package api

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"sync"
	"time"

	"github.com/gorilla/websocket"
	db "github.com/kannachi323/misty/server/internal/platform/postgres"
	"github.com/lib/pq"
)

const (
	realtimeWriteWait  = 10 * time.Second
	realtimePongWait   = 90 * time.Second
	realtimePingPeriod = 30 * time.Second
)

type TestingRealtimeClient struct {
	TestingUserID string
	conn          *websocket.Conn
	TestingSend   chan []byte
	TestingDone   chan struct{}
	once          sync.Once
	// viewingSpaceID is the space this client last told us it's actively
	// viewing (empty when not viewing any space's chat). Mutated only while
	// holding RealtimeService.mu.
	viewingSpaceID string
	// active is whether the client reported having this space's chat in
	// focus (vs. connected but idle — e.g. tabbed away). Only meaningful
	// while viewingSpaceID is non-empty. Mutated only while holding
	// RealtimeService.mu.
	active bool
}

type RealtimeService struct {
	database  *db.Database
	dsn       string
	listener  *pq.Listener
	TestingMu sync.RWMutex
	clients   map[*TestingRealtimeClient]struct{}
	// viewers maps a space ID to the set of clients currently viewing that
	// space's chat, for the "active users" presence capsule. Guarded by mu.
	TestingViewers map[string]map[*TestingRealtimeClient]struct{}
	closed         chan struct{}
	closeOnce      sync.Once
}

func NewRealtimeService(database *db.Database, dsn string) *RealtimeService {
	return &RealtimeService{
		database:       database,
		dsn:            dsn,
		clients:        map[*TestingRealtimeClient]struct{}{},
		TestingViewers: map[string]map[*TestingRealtimeClient]struct{}{},
		closed:         make(chan struct{}),
	}
}

func (s *RealtimeService) Start() error {
	if s.dsn == "" {
		return errors.New("database DSN is required for realtime")
	}
	s.listener = pq.NewListener(s.dsn, time.Second, 30*time.Second, nil)
	if err := s.listener.Listen("misty_space_events"); err != nil {
		return err
	}
	if err := s.listener.Listen("misty_space_control"); err != nil {
		return err
	}
	if err := s.listener.Ping(); err != nil {
		return err
	}
	go s.listen()
	return nil
}

func (s *RealtimeService) Close() error {
	var err error
	s.closeOnce.Do(func() {
		close(s.closed)
		if s.listener != nil {
			err = s.listener.Close()
		}
		s.TestingMu.Lock()
		for client := range s.clients {
			_ = client.conn.Close()
		}
		s.clients = map[*TestingRealtimeClient]struct{}{}
		s.TestingViewers = map[string]map[*TestingRealtimeClient]struct{}{}
		s.TestingMu.Unlock()
	})
	return err
}

func (s *RealtimeService) Health() error {
	if s == nil || s.listener == nil {
		return errors.New("realtime listener is not started")
	}
	return s.listener.Ping()
}

func (s *RealtimeService) listen() {
	for {
		select {
		case <-s.closed:
			return
		case notification, ok := <-s.listener.Notify:
			if !ok {
				return
			}
			if notification == nil {
				continue
			}
			if notification.Channel == "misty_space_control" {
				s.broadcastControl([]byte(notification.Extra))
			} else {
				eventID, err := strconv.ParseInt(notification.Extra, 10, 64)
				if err == nil {
					s.BroadcastEvent(eventID)
				}
			}
		case <-time.After(45 * time.Second):
			go s.listener.Ping()
		}
	}
}

func (s *RealtimeService) broadcastControl(payload []byte) {
	var control struct {
		Type    string   `json:"type"`
		SpaceID string   `json:"space_id"`
		UserIDs []string `json:"user_ids"`
		NoteID  string   `json:"note_id"`
		// KeepConnection marks a control message that revokes access to one
		// resource rather than to the whole Space. Losing a note grant must not
		// tear down the connection: the user is still a Space member and still
		// needs chat, tasks, and Library events.
		KeepConnection bool `json:"keep_connection"`
	}
	if json.Unmarshal(payload, &control) != nil {
		return
	}
	affected := map[string]bool{}
	for _, userID := range control.UserIDs {
		affected[userID] = true
	}
	envelopeFields := map[string]any{"type": "control", "action": control.Type, "space_id": control.SpaceID}
	if control.NoteID != "" {
		envelopeFields["note_id"] = control.NoteID
	}
	envelope, _ := json.Marshal(envelopeFields)
	s.TestingMu.RLock()
	clients := make([]*TestingRealtimeClient, 0)
	for client := range s.clients {
		if affected[client.TestingUserID] {
			clients = append(clients, client)
		}
	}
	s.TestingMu.RUnlock()
	for _, client := range clients {
		select {
		case client.TestingSend <- envelope:
		default:
		}
		if control.KeepConnection {
			continue
		}
		go func(target *TestingRealtimeClient) {
			timer := time.NewTimer(150 * time.Millisecond)
			defer timer.Stop()
			select {
			case <-timer.C:
			case <-target.TestingDone:
				return
			}
			_ = target.conn.WriteControl(websocket.CloseMessage, websocket.FormatCloseMessage(websocket.ClosePolicyViolation, "Space access changed"), time.Now().Add(time.Second))
			_ = target.conn.Close()
			s.TestingUnregister(target)
		}(client)
	}
}

func (s *RealtimeService) BroadcastEvent(eventID int64) {
	s.TestingMu.RLock()
	clients := make([]*TestingRealtimeClient, 0, len(s.clients))
	for client := range s.clients {
		clients = append(clients, client)
	}
	s.TestingMu.RUnlock()
	for _, client := range clients {
		event, err := s.database.EventByIDForUser(context.Background(), client.TestingUserID, eventID)
		if err != nil {
			continue
		}
		payload, err := json.Marshal(map[string]any{"type": "event", "event": event})
		if err != nil {
			continue
		}
		select {
		case client.TestingSend <- payload:
		default:
			_ = client.conn.WriteControl(websocket.CloseMessage, websocket.FormatCloseMessage(1013, "reconnect and resync"), time.Now().Add(time.Second))
			_ = client.conn.Close()
			s.TestingUnregister(client)
		}
	}
}

func (s *RealtimeService) Ticket() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		userID, ok := authenticatedUser(w, r, s.database)
		if !ok {
			return
		}
		var body struct {
			After int64 `json:"after"`
		}
		if r.ContentLength > 0 && decodeJSON(w, r, &body) != nil {
			return
		}
		token, hash, err := randomToken()
		if err != nil {
			writeSpaceError(w, err)
			return
		}
		if err := s.database.CreateRealtimeTicket(r.Context(), userID, hash, body.After, time.Now().UTC().Add(60*time.Second)); err != nil {
			writeSpaceError(w, err)
			return
		}
		writeJSON(w, http.StatusCreated, map[string]any{"ticket": token, "expires_in": 60})
	}
}

var realtimeUpgrader = websocket.Upgrader{
	ReadBufferSize:  4096,
	WriteBufferSize: 4096,
	CheckOrigin:     func(_ *http.Request) bool { return true }, // Single-use authenticated ticket is the CSRF boundary.
}
