package agent

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gorilla/websocket"
)

func TestVoiceWebRTCCallSidebandAndHangup(t *testing.T) {
	commands := make(chan map[string]any, 8)
	hungup := make(chan struct{}, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer fixture-key" {
			t.Error("missing server credential")
			w.WriteHeader(401)
			return
		}
		switch r.URL.Path {
		case "/realtime/calls":
			if err := r.ParseMultipartForm(100 << 10); err != nil {
				t.Error(err)
				return
			}
			if r.FormValue("sdp") != "v=0\r\nfixture-offer" {
				t.Error("missing SDP")
			}
			var config map[string]any
			if json.Unmarshal([]byte(r.FormValue("session")), &config) != nil {
				t.Error("invalid session")
			}
			input := config["audio"].(map[string]any)["input"].(map[string]any)
			if value, present := input["turn_detection"]; !present || value != nil {
				t.Error("PTT not configured")
			}
			if config["model"] != "gpt-realtime-2.1" {
				t.Error("wrong model")
			}
			w.Header().Set("Location", "/v1/realtime/calls/rtc_fixture")
			w.WriteHeader(http.StatusCreated)
			_, _ = io.WriteString(w, "v=0\r\nfixture-answer")
		case "/realtime":
			if r.URL.Query().Get("call_id") != "rtc_fixture" {
				t.Error("sideband attached to wrong call")
			}
			up := websocket.Upgrader{}
			conn, err := up.Upgrade(w, r, nil)
			if err != nil {
				t.Error(err)
				return
			}
			defer conn.Close()
			for {
				var event map[string]any
				if conn.ReadJSON(&event) != nil {
					return
				}
				commands <- event
				if event["type"] == "session.update" {
					_ = conn.WriteJSON(map[string]any{"type": "session.updated", "session": realtimeRTCConfig()})
				}
				if event["type"] == "input_audio_buffer.commit" {
					_ = conn.WriteJSON(map[string]any{"type": "conversation.item.input_audio_transcription.completed", "item_id": "input1", "transcript": "fixture", "usage": map[string]any{"type": "tokens", "input_tokens": 10, "output_tokens": 2}})
				}
			}
		case "/realtime/calls/rtc_fixture/hangup":
			hungup <- struct{}{}
			w.WriteHeader(http.StatusOK)
		default:
			t.Errorf("unexpected path %s", r.URL.Path)
			w.WriteHeader(404)
		}
	}))
	defer server.Close()
	voice, answer, err := openVoiceWebRTC(context.Background(), server.Client(), server.URL, "fixture-key", "v=0\r\nfixture-offer")
	if err != nil {
		t.Fatal(err)
	}
	defer voice.conn.Close()
	if answer != "v=0\r\nfixture-answer" {
		t.Fatal("answer changed")
	}
	if err := voice.Configure(); err != nil {
		t.Fatal(err)
	}
	if event, err := voice.Read(); err != nil || event.Type != "session-updated" || !VoiceRealtimeManualSession(event.Raw) {
		t.Fatalf("configuration: %+v %v", event, err)
	}
	if err := voice.Send(map[string]string{"type": "input-audio-commit"}); err != nil {
		t.Fatal(err)
	}
	if event, err := voice.Read(); err != nil || event.Type != "input-transcription-completed" || event.ItemID != "input1" {
		t.Fatalf("transcription: %+v %v", event, err)
	} else if usage, err := RealtimeTranscriptionUsage(event.Raw); err != nil || usage["transcription_input_tokens"] != 10 {
		t.Fatal("usage lost")
	}
	if err := voice.Send(map[string]string{"type": "response-cancel"}); err != nil {
		t.Fatal(err)
	}
	for _, expected := range []string{"session.update", "input_audio_buffer.commit", "output_audio_buffer.clear", "response.cancel"} {
		select {
		case command := <-commands:
			if command["type"] != expected {
				t.Fatalf("command %v, expected %s", command, expected)
			}
		case <-time.After(time.Second):
			t.Fatal("missing command")
		}
	}
	voice.Close()
	select {
	case <-hungup:
	case <-time.After(time.Second):
		t.Fatal("call not hung up")
	}
}

func TestVoiceWebRTCHangsUpWhenSidebandFails(t *testing.T) {
	hungup := make(chan struct{}, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/realtime/calls":
			w.Header().Set("Location", "/v1/realtime/calls/rtc_fixture")
			w.WriteHeader(201)
			_, _ = io.WriteString(w, "v=0\r\nanswer")
		case "/realtime/calls/rtc_fixture/hangup":
			hungup <- struct{}{}
		default:
			w.WriteHeader(503)
		}
	}))
	defer server.Close()
	if voice, _, err := openVoiceWebRTC(context.Background(), server.Client(), server.URL, "fixture", "v=0\r\noffer"); err == nil || voice != nil {
		t.Fatal("failed control accepted")
	}
	select {
	case <-hungup:
	case <-time.After(time.Second):
		t.Fatal("abandoned call not closed before fallback")
	}
}
