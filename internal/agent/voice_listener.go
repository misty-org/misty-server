package agent

import (
	"context"
	"encoding/base64"
	"errors"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/gorilla/websocket"
	"github.com/kannachi323/misty/server/internal/platform/config"
)

// The voice pipeline listens with ElevenLabs Scribe v2 Realtime. It is a live
// socket, so like OpenAI Realtime it is one of the few model connections Go
// holds itself: audio is transcribed while the user is still speaking. Misty's
// own key is used, never a user's.

const elevenLabsListenEndpoint = "wss://api.elevenlabs.io/v1/speech-to-text/realtime"

// ElevenLabsConfigured reports whether the instance has an ElevenLabs key.
func ElevenLabsConfigured() bool {
	return strings.TrimSpace(config.Getenv("ELEVENLABS_API_KEY")) != ""
}

// speechListener is one spoken turn's transcription.
type speechListener interface {
	Write(pcm []byte) error
	Finish(ctx context.Context) (string, error)
	Close()
}

type elevenLabsListener struct {
	conn *websocket.Conn
}

// openElevenLabsListener starts a manually committed stream of 24 kHz 16-bit
// mono PCM, the format the voice session already carries.
func openElevenLabsListener(ctx context.Context, endpoint string) (speechListener, error) {
	key := strings.TrimSpace(config.Getenv("ELEVENLABS_API_KEY"))
	if key == "" {
		return nil, errors.New("speech-to-text is not configured")
	}
	if endpoint == "" {
		endpoint = elevenLabsListenEndpoint
	}
	u, err := url.Parse(endpoint)
	if err != nil {
		return nil, err
	}
	query := u.Query()
	query.Set("model_id", "scribe_v2_realtime")
	query.Set("audio_format", "pcm_24000")
	query.Set("commit_strategy", "manual")
	u.RawQuery = query.Encode()
	setup, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	dialer := websocket.Dialer{HandshakeTimeout: 10 * time.Second, Proxy: http.ProxyFromEnvironment}
	conn, response, err := dialer.DialContext(setup, u.String(), http.Header{"xi-api-key": {key}})
	if response != nil && response.Body != nil {
		response.Body.Close()
	}
	if err != nil {
		return nil, errors.New("speech-to-text connection failed")
	}
	conn.SetReadLimit(1 << 20)
	return &elevenLabsListener{conn: conn}, nil
}

func (l *elevenLabsListener) send(pcm []byte, commit bool) error {
	_ = l.conn.SetWriteDeadline(time.Now().Add(5 * time.Second))
	return l.conn.WriteJSON(map[string]any{"message_type": "input_audio_chunk", "audio_base_64": base64.StdEncoding.EncodeToString(pcm), "commit": commit, "sample_rate": 24000})
}

func (l *elevenLabsListener) Write(pcm []byte) error { return l.send(pcm, false) }

// Finish commits the turn and returns its final transcript.
func (l *elevenLabsListener) Finish(ctx context.Context) (string, error) {
	defer l.conn.Close()
	if err := l.send(nil, true); err != nil {
		return "", errors.New("speech-to-text did not accept the end of speech")
	}
	deadline := time.Now().Add(15 * time.Second)
	if d, ok := ctx.Deadline(); ok && d.Before(deadline) {
		deadline = d
	}
	_ = l.conn.SetReadDeadline(deadline)
	for {
		var message struct {
			Type  string `json:"message_type"`
			Text  string `json:"text"`
			Error string `json:"error"`
		}
		if err := l.conn.ReadJSON(&message); err != nil {
			return "", errors.New("speech-to-text ended without a transcript")
		}
		switch message.Type {
		case "committed_transcript", "committed_transcript_with_timestamps":
			return strings.TrimSpace(message.Text), nil
		case "session_started", "partial_transcript", "warning", "committed_transcript_entities", "edited_transcript":
			continue
		case "insufficient_audio_activity":
			// Nothing intelligible was said; an empty transcript, not a failure.
			return "", nil
		default:
			return "", errors.New("speech-to-text rejected the audio: " + message.Type)
		}
	}
}

func (l *elevenLabsListener) Close() { _ = l.conn.Close() }
