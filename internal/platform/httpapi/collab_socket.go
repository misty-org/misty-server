package api

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/gorilla/websocket"

	"github.com/misty-org/misty-server/internal/collab"
	"github.com/misty-org/misty-server/internal/collab/wire"
)

const (
	collabWriteWait   = 10 * time.Second
	collabPongWait    = 90 * time.Second
	collabPingPeriod  = 30 * time.Second
	collabCloseWait   = 5 * time.Second
	collabSendBacklog = 1024
)

var errCollabBacklog = errors.New("collab: the connection is not keeping up")

var collabUpgrader = websocket.Upgrader{
	ReadBufferSize:  16 * 1024,
	WriteBufferSize: 16 * 1024,
	CheckOrigin:     func(_ *http.Request) bool { return true },
}

func newCollabConnID() string {
	raw := make([]byte, 16)
	_, _ = rand.Read(raw)
	return "cn_" + hex.EncodeToString(raw)
}

type collabOutbound struct {
	frame  wire.Frame
	close  bool
	code   int
	reason string
}

type collabSocket struct {
	id     string
	out    chan collabOutbound
	once   sync.Once
	closed chan struct{}
}

func (c *collabSocket) ID() string { return c.id }

func (c *collabSocket) Send(frame wire.Frame) error {
	select {
	case <-c.closed:
		return nil
	default:
	}
	select {
	case c.out <- collabOutbound{frame: frame}:
		return nil
	default:
		c.Close(wire.CloseInternal, "slow")
		return errCollabBacklog
	}
}

func (c *collabSocket) Close(code int, reason string) {
	c.once.Do(func() {
		close(c.closed)
		select {
		case c.out <- collabOutbound{close: true, code: code, reason: reason}:
		default:
		}
	})
}

func bearerToken(r *http.Request) string {
	header := strings.TrimSpace(r.Header.Get("Authorization"))
	if len(header) > 7 && strings.EqualFold(header[:7], "bearer ") {
		return strings.TrimSpace(header[7:])
	}
	return ""
}

func (t *CollabTransports) Socket() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !websocket.IsWebSocketUpgrade(r) {
			w.Header().Set("Upgrade", "websocket")
			writeJSON(w, http.StatusUpgradeRequired, map[string]string{"code": "websocket_upgrade_required"})
			return
		}
		ws, err := collabUpgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		conn := &collabSocket{id: newCollabConnID(), out: make(chan collabOutbound, collabSendBacklog), closed: make(chan struct{})}
		session := t.Rooms.Open(conn, bearerToken(r))
		done := make(chan struct{})
		read := make(chan struct{})
		go collabSocketWriter(ws, conn, done, read)
		ws.SetReadLimit(wire.MaxFrameBytes + 1024)
		_ = ws.SetReadDeadline(time.Now().Add(collabPongWait))
		ws.SetPongHandler(func(string) error { return ws.SetReadDeadline(time.Now().Add(collabPongWait)) })
		for {
			kind, data, err := ws.ReadMessage()
			if err != nil {
				var closeErr *websocket.CloseError
				if errors.As(err, &closeErr) || errors.Is(err, websocket.ErrReadLimit) {
					if errors.Is(err, websocket.ErrReadLimit) {
						conn.Close(wire.CloseTooLarge, "too_large")
					}
				}
				break
			}
			if kind != websocket.TextMessage && kind != websocket.BinaryMessage {
				continue
			}
			session.Receive(wire.Frame{Text: kind == websocket.TextMessage, Data: data})
		}
		close(read)
		session.Closed()
		conn.Close(wire.CloseNormal, "normal")
		<-done
	}
}

func collabSocketClosing(ws *websocket.Conn, code int, reason string, read <-chan struct{}) {
	_ = ws.WriteControl(websocket.CloseMessage, websocket.FormatCloseMessage(code, reason), time.Now().Add(collabWriteWait))
	wait := time.NewTimer(collabCloseWait)
	defer wait.Stop()
	select {
	case <-read:
	case <-wait.C:
	}
}

func collabSocketWriter(ws *websocket.Conn, conn *collabSocket, done chan struct{}, read <-chan struct{}) {
	defer close(done)
	defer ws.Close()
	ping := time.NewTicker(collabPingPeriod)
	defer ping.Stop()
	for {
		select {
		case message := <-conn.out:
			_ = ws.SetWriteDeadline(time.Now().Add(collabWriteWait))
			if message.close {
				collabSocketClosing(ws, message.code, message.reason, read)
				return
			}
			kind := websocket.BinaryMessage
			if message.frame.Text {
				kind = websocket.TextMessage
			}
			if err := ws.WriteMessage(kind, message.frame.Data); err != nil {
				conn.Close(wire.CloseInternal, "internal")
				return
			}
		case <-conn.closed:
			for {
				select {
				case message := <-conn.out:
					if message.close {
						collabSocketClosing(ws, message.code, message.reason, read)
						return
					}
					kind := websocket.BinaryMessage
					if message.frame.Text {
						kind = websocket.TextMessage
					}
					_ = ws.SetWriteDeadline(time.Now().Add(collabWriteWait))
					if ws.WriteMessage(kind, message.frame.Data) != nil {
						return
					}
				default:
					return
				}
			}
		case <-ping.C:
			if err := ws.WriteControl(websocket.PingMessage, nil, time.Now().Add(collabWriteWait)); err != nil {
				conn.Close(wire.CloseInternal, "internal")
				return
			}
		}
	}
}

type CollabTransports struct {
	Rooms   *collab.Rooms
	streams sync.Map
}
