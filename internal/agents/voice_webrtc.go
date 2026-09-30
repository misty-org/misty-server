package agent

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"mime/multipart"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"

	"github.com/gorilla/websocket"
	envconfig "github.com/kannachi323/misty/server/internal/platform/config"
)

// WebRTC uses a server-authenticated call and sideband control connection. The
// desktop receives SDP only; neither a provider key nor a client secret escapes.
type VoiceWebRTC struct {
	conn              *websocket.Conn
	client            *http.Client
	key, base, callID string
}

func VoiceWebRTCConfigured() bool {
	return strings.TrimSpace(envconfig.Getenv("MISTY_REALTIME_API_KEY")) != ""
}

func realtimeRTCConfig() map[string]any {
	return map[string]any{
		"type": "realtime", "model": strings.TrimPrefix(AgentRealtimeModel, "openai/"),
		"instructions":      "You are Misty's voice interface. Speak only the confirmed reply supplied by the server. No tools are available in this voice session.",
		"output_modalities": []string{"audio"}, "max_output_tokens": 4096,
		"tools": []any{},
		"audio": map[string]any{
			"input":  map[string]any{"transcription": map[string]string{"model": AgentRealtimeTranscriptionModel}, "turn_detection": nil},
			"output": map[string]string{"voice": "alloy"},
		},
	}
}

func (a *SmartLibraryAnalyzer) OpenVoiceWebRTC(ctx context.Context, offer string) (*VoiceWebRTC, string, error) {
	if !VoiceWebRTCConfigured() {
		return nil, "", errors.New("WebRTC is not configured")
	}
	return openVoiceWebRTC(ctx, http.DefaultClient, "https://api.openai.com/v1", strings.TrimSpace(envconfig.Getenv("MISTY_REALTIME_API_KEY")), offer)
}

func openVoiceWebRTC(ctx context.Context, client *http.Client, base, key, offer string) (*VoiceWebRTC, string, error) {
	if len(offer) > 65536 || !strings.HasPrefix(offer, "v=0") {
		return nil, "", errors.New("invalid voice SDP")
	}
	setup, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	var body bytes.Buffer
	form := multipart.NewWriter(&body)
	_ = form.WriteField("sdp", offer)
	config, _ := json.Marshal(realtimeRTCConfig())
	_ = form.WriteField("session", string(config))
	_ = form.Close()
	req, err := http.NewRequestWithContext(setup, http.MethodPost, base+"/realtime/calls", &body)
	if err != nil {
		return nil, "", err
	}
	req.Header.Set("Authorization", "Bearer "+key)
	req.Header.Set("Content-Type", form.FormDataContentType())
	response, err := client.Do(req)
	if err != nil {
		return nil, "", errors.New("WebRTC call setup failed")
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusCreated {
		return nil, "", &SpeechProviderError{Status: response.StatusCode}
	}
	location, err := url.Parse(response.Header.Get("Location"))
	if err != nil {
		return nil, "", errors.New("invalid WebRTC call location")
	}
	parts := strings.Split(strings.TrimRight(location.Path, "/"), "/")
	callID := parts[len(parts)-1]
	if !regexp.MustCompile(`^rtc_[a-zA-Z0-9_-]{1,180}$`).MatchString(callID) {
		return nil, "", errors.New("invalid WebRTC call ID")
	}
	voice := &VoiceWebRTC{client: client, base: base, key: key, callID: callID}
	answer, err := io.ReadAll(io.LimitReader(response.Body, 65537))
	if err != nil || len(answer) > 65536 || !bytes.HasPrefix(answer, []byte("v=0")) {
		voice.Close()
		return nil, "", errors.New("invalid WebRTC answer")
	}
	sideband, _ := url.Parse(base + "/realtime")
	sideband.Scheme = "wss"
	if strings.HasPrefix(base, "http://") {
		sideband.Scheme = "ws"
	}
	query := sideband.Query()
	query.Set("call_id", callID)
	sideband.RawQuery = query.Encode()
	dialer := websocket.Dialer{HandshakeTimeout: 10 * time.Second, Proxy: http.ProxyFromEnvironment}
	conn, resp, err := dialer.DialContext(setup, sideband.String(), http.Header{"Authorization": []string{"Bearer " + key}})
	if err != nil {
		if resp != nil {
			resp.Body.Close()
		}
		voice.Close()
		return nil, "", errors.New("WebRTC control connection failed")
	}
	conn.SetReadLimit(2 << 20)
	voice.conn = conn
	return voice, string(answer), nil
}

func (v *VoiceWebRTC) WebRTC() bool { return true }
func (v *VoiceWebRTC) Close() {
	if v.conn != nil {
		_ = v.conn.Close()
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, v.base+"/realtime/calls/"+v.callID+"/hangup", nil)
	if err != nil {
		return
	}
	req.Header.Set("Authorization", "Bearer "+v.key)
	if resp, err := v.client.Do(req); err == nil {
		resp.Body.Close()
	}
}
func (v *VoiceWebRTC) Configure() error {
	config := realtimeRTCConfig()
	delete(config, "model") // The model is fixed when the call is created.
	return v.write(map[string]any{"type": "session.update", "session": config})
}
func (v *VoiceWebRTC) write(event any) error {
	_ = v.conn.SetWriteDeadline(time.Now().Add(10 * time.Second))
	return v.conn.WriteJSON(event)
}
func (v *VoiceWebRTC) Send(event any) error {
	encoded, _ := json.Marshal(event)
	var command struct {
		Type string `json:"type"`
		Item struct {
			Text string `json:"text"`
		} `json:"item"`
		Options struct {
			Instructions string `json:"instructions"`
		} `json:"options"`
	}
	if err := json.Unmarshal(encoded, &command); err != nil {
		return err
	}
	switch command.Type {
	case "input-audio-clear":
		return v.write(map[string]string{"type": "input_audio_buffer.clear"})
	case "input-audio-commit":
		return v.write(map[string]string{"type": "input_audio_buffer.commit"})
	case "response-cancel":
		_ = v.write(map[string]string{"type": "output_audio_buffer.clear"})
		return v.write(map[string]string{"type": "response.cancel"})
	case "conversation-item-create":
		return v.write(map[string]any{"type": "conversation.item.create", "item": map[string]any{
			"type": "message", "role": "user", "content": []any{map[string]string{"type": "input_text", "text": command.Item.Text}},
		}})
	case "response-create":
		return v.write(map[string]any{"type": "response.create", "response": map[string]any{
			"output_modalities": []string{"audio"}, "instructions": command.Options.Instructions,
		}})
	default:
		return errors.New("unsupported WebRTC voice command")
	}
}

func (v *VoiceWebRTC) Read() (VoiceRealtimeEvent, error) {
	var raw json.RawMessage
	if err := v.conn.ReadJSON(&raw); err != nil {
		return VoiceRealtimeEvent{}, err
	}
	var wire struct {
		Type       string `json:"type"`
		ItemID     string `json:"item_id"`
		ResponseID string `json:"response_id"`
		Transcript string `json:"transcript"`
		Response   struct {
			ID     string `json:"id"`
			Status string `json:"status"`
		} `json:"response"`
	}
	if err := json.Unmarshal(raw, &wire); err != nil {
		return VoiceRealtimeEvent{}, err
	}
	event := VoiceRealtimeEvent{Raw: raw, ItemID: wire.ItemID, ResponseID: wire.ResponseID, Transcript: wire.Transcript}
	event.Type = map[string]string{
		"session.updated": "session-updated", "input_audio_buffer.cleared": "audio-cleared",
		"input_audio_buffer.committed":                          "audio-committed",
		"conversation.item.input_audio_transcription.completed": "input-transcription-completed",
		"conversation.item.input_audio_transcription.failed":    "error",
		"response.created":                                      "response-created", "response.done": "response-done",
		"output_audio_buffer.started": "playback-started", "output_audio_buffer.stopped": "playback-stopped",
		"input_audio_buffer.speech_started": "speech-started", "error": "error",
	}[wire.Type]
	if wire.Response.ID != "" {
		event.ResponseID, event.Status = wire.Response.ID, wire.Response.Status
	}
	return event, nil
}
