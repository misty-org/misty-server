package api

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"mime"
	"net/http"
	"sync"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/misty-org/misty-server/internal/collab"
	"github.com/misty-org/misty-server/internal/collab/wire"
)

const (
	collabStreamKeep      = 25 * time.Second
	collabOpenBodyLimit   = 64 * 1024
	collabStreamFrameSize = wire.MaxFrameBytes
)

type collabStream struct {
	collabSocket
	session *collab.Session
	who     string
	receive sync.Mutex
}

func (t *CollabTransports) Stream() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		collabCORS(w)
		flusher, ok := w.(http.Flusher)
		if !ok {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"code": "streaming_unsupported"})
			return
		}
		token := bearerToken(r)
		who, err := t.Rooms.Identify(token)
		if err != nil {
			writeJSON(w, http.StatusUnauthorized, map[string]string{"code": "unauthorized"})
			return
		}
		body, err := io.ReadAll(io.LimitReader(r.Body, collabOpenBodyLimit+1))
		if err != nil || len(body) > collabOpenBodyLimit {
			writeJSON(w, http.StatusBadRequest, map[string]string{"code": "bad_open"})
			return
		}
		var open map[string]json.RawMessage
		if json.Unmarshal(body, &open) != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"code": "bad_open"})
			return
		}
		delete(open, "token")
		if _, has := open["type"]; !has {
			open["type"] = json.RawMessage(`"open"`)
		}
		opening, _ := json.Marshal(open)
		stream := &collabStream{collabSocket: collabSocket{id: newCollabConnID(),
			out: make(chan collabOutbound, collabSendBacklog), closed: make(chan struct{})}, who: who}
		stream.session = t.Rooms.Open(stream, token)
		t.streams.Store(stream.id, stream)
		defer t.streams.Delete(stream.id)
		w.Header().Set("Content-Type", "text/event-stream")
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("X-Accel-Buffering", "no")
		w.WriteHeader(http.StatusOK)
		flusher.Flush()
		stream.session.Receive(wire.Frame{Text: true, Data: opening})
		keep := time.NewTicker(collabStreamKeep)
		defer keep.Stop()
		defer stream.session.Closed()
		opened := false
		for {
			select {
			case <-r.Context().Done():
				stream.Close(wire.CloseNormal, "normal")
				return
			case <-keep.C:
				if _, err := io.WriteString(w, ": keep\n\n"); err != nil {
					stream.Close(wire.CloseNormal, "normal")
					return
				}
				flusher.Flush()
			case message := <-stream.out:
				if message.close {
					writeCollabEvent(w, "close", mustRawJSON(map[string]any{"code": message.code, "reason": message.reason}))
					flusher.Flush()
					return
				}
				if !opened && message.frame.Text && isOpened(message.frame.Data) {
					opened = true
					writeCollabEvent(w, "opened", withConn(message.frame.Data, stream.id))
				} else if message.frame.Text {
					writeCollabEvent(w, "t", message.frame.Data)
				} else {
					writeCollabEvent(w, "b", []byte(base64.StdEncoding.EncodeToString(message.frame.Data)))
				}
				flusher.Flush()
			case <-stream.closed:
				for {
					select {
					case message := <-stream.out:
						if message.close {
							writeCollabEvent(w, "close", mustRawJSON(map[string]any{"code": message.code, "reason": message.reason}))
							flusher.Flush()
							return
						}
						if message.frame.Text {
							writeCollabEvent(w, "t", message.frame.Data)
						} else {
							writeCollabEvent(w, "b", []byte(base64.StdEncoding.EncodeToString(message.frame.Data)))
						}
					default:
						flusher.Flush()
						return
					}
				}
			}
		}
	}
}

func isOpened(data []byte) bool {
	var probe struct {
		Type string `json:"type"`
	}
	return json.Unmarshal(data, &probe) == nil && probe.Type == "opened"
}

func withConn(data []byte, id string) []byte {
	fields := map[string]json.RawMessage{}
	if json.Unmarshal(data, &fields) != nil {
		return data
	}
	fields["conn"], _ = json.Marshal(id)
	out, err := json.Marshal(fields)
	if err != nil {
		return data
	}
	return out
}

func writeCollabEvent(w io.Writer, event string, data []byte) {
	var buffer bytes.Buffer
	fmt.Fprintf(&buffer, "event: %s\n", event)
	for _, line := range bytes.Split(data, []byte("\n")) {
		buffer.WriteString("data: ")
		buffer.Write(line)
		buffer.WriteString("\n")
	}
	buffer.WriteString("\n")
	_, _ = w.Write(buffer.Bytes())
}

func (t *CollabTransports) StreamFrame() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		collabCORS(w)
		value, ok := t.streams.Load(chi.URLParam(r, "conn"))
		if !ok {
			writeJSON(w, http.StatusNotFound, map[string]string{"code": "not_found"})
			return
		}
		stream := value.(*collabStream)
		who, err := t.Rooms.Identify(bearerToken(r))
		if err != nil {
			writeJSON(w, http.StatusUnauthorized, map[string]string{"code": "unauthorized"})
			return
		}
		if who != stream.who {
			writeJSON(w, http.StatusForbidden, map[string]string{"code": "forbidden"})
			return
		}
		body, err := io.ReadAll(io.LimitReader(r.Body, collabStreamFrameSize+1))
		if err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"code": "bad_frame"})
			return
		}
		if len(body) > collabStreamFrameSize {
			writeJSON(w, http.StatusRequestEntityTooLarge, map[string]string{"code": "too_large"})
			return
		}
		mediaType, _, _ := mime.ParseMediaType(r.Header.Get("Content-Type"))
		frame := wire.Frame{Data: body}
		switch mediaType {
		case "application/octet-stream":
		case "application/json":
			frame.Text = true
		default:
			writeJSON(w, http.StatusUnsupportedMediaType, map[string]string{"code": "unsupported_media_type"})
			return
		}
		select {
		case <-stream.closed:
			writeJSON(w, http.StatusNotFound, map[string]string{"code": "not_found"})
			return
		default:
		}
		stream.receive.Lock()
		stream.session.Receive(frame)
		stream.receive.Unlock()
		w.WriteHeader(http.StatusNoContent)
	}
}

func collabCORS(w http.ResponseWriter) {
	w.Header().Set("Access-Control-Allow-Origin", "*")
	w.Header().Set("Access-Control-Allow-Headers", "Authorization, Content-Type")
	w.Header().Set("Access-Control-Allow-Methods", "GET, POST, OPTIONS")
	w.Header().Set("Access-Control-Expose-Headers", "Location")
}

func (t *CollabTransports) Preflight() http.HandlerFunc {
	return func(w http.ResponseWriter, _ *http.Request) {
		collabCORS(w)
		w.Header().Set("Access-Control-Max-Age", "600")
		w.WriteHeader(http.StatusNoContent)
	}
}
