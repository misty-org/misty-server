package agent

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/gorilla/websocket"
)

const openAIRealtimeBase = "https://api.openai.com/v1"

func openOpenAIRealtimeModel(ctx context.Context, base, key, model string) (*VoiceRealtime, error) {
	endpoint, err := url.Parse(strings.TrimRight(base, "/") + "/realtime")
	if err != nil || endpoint.Host == "" || key == "" {
		return nil, errors.New("direct OpenAI realtime is not configured")
	}
	if endpoint.Scheme == "https" {
		endpoint.Scheme = "wss"
	} else if endpoint.Scheme == "http" {
		endpoint.Scheme = "ws"
	} else {
		return nil, errors.New("invalid OpenAI realtime URL")
	}
	query := endpoint.Query()
	query.Set("model", strings.TrimPrefix(model, "openai/"))
	endpoint.RawQuery = query.Encode()
	setup, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	dialer := websocket.Dialer{HandshakeTimeout: 15 * time.Second, Proxy: http.ProxyFromEnvironment}
	conn, response, err := dialer.DialContext(setup, endpoint.String(), http.Header{"Authorization": []string{"Bearer " + key}})
	if err != nil {
		if response != nil {
			response.Body.Close()
		}
		return nil, errors.New("OpenAI realtime websocket failed")
	}
	conn.SetReadLimit(2 << 20)
	return &VoiceRealtime{conn: conn, openAI: true, model: model}, nil
}

// Only the server's bounded companion commands can reach the provider.
func openAIRealtimeCommand(event any) (map[string]any, error) {
	encoded, err := json.Marshal(event)
	if err != nil {
		return nil, err
	}
	var command struct {
		Type         string `json:"type"`
		Audio        string `json:"audio"`
		ItemID       string `json:"itemId"`
		ContentIndex int    `json:"contentIndex"`
		AudioEndMS   int    `json:"audioEndMs"`
		Item         struct {
			Type, Role, Text, Output string
			CallID                   string `json:"callId"`
		} `json:"item"`
		Options map[string]any `json:"options"`
		Config  map[string]any `json:"config"`
	}
	if err := json.Unmarshal(encoded, &command); err != nil {
		return nil, err
	}
	switch command.Type {
	case "session-update":
		session := map[string]any{"type": "realtime"}
		for _, key := range []string{"instructions", "tools"} {
			if value, ok := command.Config[key]; ok {
				session[key] = value
			}
		}
		if value, ok := command.Config["outputModalities"]; ok {
			session["output_modalities"] = value
		}
		if options, ok := command.Config["providerOptions"].(map[string]any); ok {
			if value, ok := options["max_output_tokens"]; ok {
				session["max_output_tokens"] = value
			}
		}
		input, output := map[string]any{}, map[string]any{}
		if value, ok := command.Config["inputAudioFormat"]; ok {
			input["format"] = value
		}
		if value, ok := command.Config["inputAudioTranscription"]; ok {
			input["transcription"] = value
		}
		if _, ok := command.Config["turnDetection"]; ok {
			input["turn_detection"] = nil
		}
		if value, ok := command.Config["outputAudioFormat"]; ok {
			output["format"] = value
		}
		if value, ok := command.Config["voice"]; ok {
			output["voice"] = value
		}
		audio := map[string]any{}
		if len(input) > 0 {
			audio["input"] = input
		}
		if len(output) > 0 {
			audio["output"] = output
		}
		if len(audio) > 0 {
			session["audio"] = audio
		}
		return map[string]any{"type": "session.update", "session": session}, nil
	case "input-audio-append":
		return map[string]any{"type": "input_audio_buffer.append", "audio": command.Audio}, nil
	case "input-audio-clear", "input-audio-commit":
		return map[string]any{"type": map[string]string{"input-audio-clear": "input_audio_buffer.clear", "input-audio-commit": "input_audio_buffer.commit"}[command.Type]}, nil
	case "response-cancel":
		return map[string]any{"type": "response.cancel"}, nil
	case "response-create":
		response := map[string]any{}
		if value, ok := command.Options["instructions"]; ok {
			response["instructions"] = value
		}
		if value, ok := command.Options["modalities"]; ok {
			response["output_modalities"] = value
		}
		return map[string]any{"type": "response.create", "response": response}, nil
	case "conversation-item-create":
		var item map[string]any
		switch command.Item.Type {
		case "function-call-output":
			item = map[string]any{"type": "function_call_output", "call_id": command.Item.CallID, "output": command.Item.Output}
		case "text-message":
			item = map[string]any{"type": "message", "role": command.Item.Role, "content": []any{map[string]string{"type": "input_text", "text": command.Item.Text}}}
		default:
			return nil, errors.New("unsupported OpenAI realtime item")
		}
		return map[string]any{"type": "conversation.item.create", "item": item}, nil
	case "conversation-item-truncate":
		return map[string]any{"type": "conversation.item.truncate", "item_id": command.ItemID, "content_index": command.ContentIndex, "audio_end_ms": command.AudioEndMS}, nil
	default:
		return nil, errors.New("unsupported OpenAI realtime command")
	}
}

func decodeOpenAIRealtimeEvent(raw json.RawMessage) (VoiceRealtimeEvent, error) {
	var wire struct {
		Type                               string `json:"type"`
		ItemID                             string `json:"item_id"`
		ResponseID                         string `json:"response_id"`
		CallID                             string `json:"call_id"`
		Name, Arguments, Transcript, Delta string
		Response                           struct{ ID, Status string } `json:"response"`
		Error                              struct{ Code string }       `json:"error"`
	}
	if err := json.Unmarshal(raw, &wire); err != nil {
		return VoiceRealtimeEvent{}, err
	}
	event := VoiceRealtimeEvent{Raw: raw, ItemID: wire.ItemID, ResponseID: wire.ResponseID, CallID: wire.CallID, Name: wire.Name, Arguments: wire.Arguments, Transcript: wire.Transcript, Delta: wire.Delta, Code: wire.Error.Code}
	event.Type = map[string]string{
		"session.created": "session-created", "session.updated": "session-updated",
		"input_audio_buffer.cleared": "audio-cleared", "input_audio_buffer.committed": "audio-committed",
		"conversation.item.input_audio_transcription.completed": "input-transcription-completed",
		"conversation.item.input_audio_transcription.failed":    "input-transcription-failed",
		"response.created": "response-created", "response.done": "response-done",
		"response.output_audio.delta": "audio-delta", "response.output_audio_transcript.delta": "audio-transcript-delta",
		"response.function_call_arguments.done": "function-call-arguments-done",
		"input_audio_buffer.speech_started":     "speech-started", "error": "error",
	}[wire.Type]
	if wire.Response.ID != "" {
		event.ResponseID, event.Status = wire.Response.ID, wire.Response.Status
	}
	return event, nil
}
