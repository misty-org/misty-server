package agent

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gorilla/websocket"
)

func TestAccountOpenAICompanionConfigurationToolsAndMeasuredUsage(t *testing.T) {
	commands := make(chan map[string]any, 8)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/realtime" || r.URL.Query().Get("model") != "gpt-realtime-2.1-mini" || r.Header.Get("Authorization") != "Bearer openai-fixture" || r.Header.Get("ai-gateway-auth-method") != "" {
			t.Error("incorrect account realtime route or credential")
			w.WriteHeader(401)
			return
		}
		conn, err := (&websocket.Upgrader{}).Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer conn.Close()
		_ = conn.SetReadDeadline(time.Now().Add(5 * time.Second))
		for {
			var command map[string]any
			if conn.ReadJSON(&command) != nil {
				return
			}
			commands <- command
			if command["type"] == "session.update" {
				_ = conn.WriteJSON(map[string]any{"type": "session.updated", "session": command["session"]})
			}
			if command["type"] == "response.create" {
				_ = conn.WriteJSON(map[string]any{"type": "response.function_call_arguments.done", "call_id": "call-1", "name": "get_task_status", "arguments": "{}"})
				_ = conn.WriteJSON(map[string]any{"type": "response.done", "response": map[string]any{"id": "response-1", "status": "completed", "usage": map[string]any{"input_tokens": 10, "output_tokens": 2, "input_token_details": map[string]any{"text_tokens": 10, "audio_tokens": 0, "cached_tokens": 0, "cached_tokens_details": map[string]any{"text_tokens": 0, "audio_tokens": 0}}, "output_token_details": map[string]any{"text_tokens": 2, "audio_tokens": 0}}}})
			}
		}
	}))
	defer server.Close()
	voice, err := openOpenAIRealtimeModel(context.Background(), server.URL, "openai-fixture", "openai/gpt-realtime-2.1-mini")
	if err != nil {
		t.Fatal(err)
	}
	defer voice.Close()
	_ = voice.conn.SetReadDeadline(time.Now().Add(5 * time.Second))
	if err := voice.Send(VoiceConversationConfig("[]")); err != nil {
		t.Fatal(err)
	}
	event, err := voice.Read()
	if err != nil || event.Type != "session-updated" || !VoiceRealtimeManualSession(event.Raw) || !VoiceRealtimeOutputLimit(event.Raw, VoiceConversationOutputTokens) {
		t.Fatalf("configuration: %+v %v", event, err)
	}
	config := (<-commands)["session"].(map[string]any)
	if len(config["tools"].([]any)) != 5 {
		t.Fatal("companion tool declarations lost")
	}
	if err := voice.Send(map[string]any{"type": "conversation-item-create", "item": map[string]string{"type": "text-message", "role": "user", "text": "Check my task"}}); err != nil {
		t.Fatal(err)
	}
	if command := <-commands; command["item"].(map[string]any)["type"] != "message" {
		t.Fatal("typed message was not translated")
	}
	if err := voice.Send(map[string]string{"type": "response-create"}); err != nil {
		t.Fatal(err)
	}
	<-commands
	event, err = voice.Read()
	if err != nil || event.Type != "function-call-arguments-done" || event.CallID != "call-1" || event.Name != "get_task_status" || event.Arguments != "{}" {
		t.Fatalf("tool: %+v %v", event, err)
	}
	event, err = voice.Read()
	usage, usageErr := RealtimeResponseUsage(event.Raw)
	if err != nil || usageErr != nil || event.Status != "completed" || usage["input_text_tokens"] != 10 {
		t.Fatalf("usage: %+v %v %v", usage, err, usageErr)
	}
	if err := voice.Send(map[string]any{"type": "conversation-item-create", "item": map[string]string{"type": "function-call-output", "callId": "call-1", "output": `{"state":"completed"}`}}); err != nil {
		t.Fatal(err)
	}
	if item := (<-commands)["item"].(map[string]any); item["type"] != "function_call_output" || item["call_id"] != "call-1" {
		t.Fatal("tool result identity lost")
	}
	if err := voice.Send(map[string]string{"type": "response-cancel"}); err != nil {
		t.Fatal(err)
	}
	if command := <-commands; command["type"] != "response.cancel" {
		t.Fatal("cancellation was not translated")
	}
}

func TestOpenAIRealtimeAudioAndTruncationCodec(t *testing.T) {
	for _, raw := range []string{
		`{"type":"response.output_audio.delta","item_id":"item-1","delta":"AAAA"}`,
		`{"type":"response.output_audio_transcript.delta","item_id":"item-1","delta":"Hello"}`,
	} {
		event, err := decodeOpenAIRealtimeEvent(json.RawMessage(raw))
		if err != nil || event.ItemID != "item-1" || event.Delta == "" || event.Type == "" {
			t.Fatal("audio event lost", event, err)
		}
	}
	command, err := openAIRealtimeCommand(map[string]any{"type": "conversation-item-truncate", "itemId": "item-1", "contentIndex": 0, "audioEndMs": 250})
	if err != nil || command["type"] != "conversation.item.truncate" || command["item_id"] != "item-1" || command["audio_end_ms"] != 250 {
		t.Fatal("playback boundary lost", command, err)
	}
	if _, err := openAIRealtimeCommand(map[string]string{"type": "unknown"}); err == nil {
		t.Fatal("unsupported command accepted")
	}
}
