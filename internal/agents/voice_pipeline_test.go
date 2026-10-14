package agent

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"github.com/kannachi323/misty/server/internal/modelruntime"
)

// fakeListener plays ElevenLabs realtime speech-to-text: it checks the key and
// stream settings, then answers the manual commit with a committed transcript.
func fakeListener(t *testing.T, transcript string) string {
	t.Helper()
	upgrader := websocket.Upgrader{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		if r.Header.Get("xi-api-key") != "elevenlabs-test-key" || q.Get("audio_format") != "pcm_24000" || q.Get("commit_strategy") != "manual" || q.Get("model_id") != "scribe_v2_realtime" {
			http.Error(w, "unauthorized or wrong stream", http.StatusUnauthorized)
			return
		}
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer conn.Close()
		_ = conn.WriteJSON(map[string]any{"message_type": "session_started", "session_id": "s"})
		for {
			var chunk struct {
				Type       string `json:"message_type"`
				Audio      string `json:"audio_base_64"`
				Commit     bool   `json:"commit"`
				SampleRate int    `json:"sample_rate"`
			}
			if conn.ReadJSON(&chunk) != nil {
				return
			}
			if chunk.Type != "input_audio_chunk" || chunk.SampleRate != 24000 {
				t.Error("bad audio chunk", chunk.Type, chunk.SampleRate)
				return
			}
			if chunk.Commit {
				// A slow transcript: the reply must still wait for it.
				time.Sleep(300 * time.Millisecond)
				_ = conn.WriteJSON(map[string]any{"message_type": "partial_transcript", "text": "hello"})
				_ = conn.WriteJSON(map[string]any{"message_type": "committed_transcript", "text": transcript})
				return
			}
		}
	}))
	t.Cleanup(server.Close)
	return "ws" + strings.TrimPrefix(server.URL, "http")
}

// fakeRuntime answers text calls with the queued turns and speaks fixed PCM.
func fakeRuntime(t *testing.T, turns ...string) *modelruntime.Client {
	t.Helper()
	var heardBy []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/v1/models/text":
			var request modelruntime.TextRequest
			_ = json.NewDecoder(r.Body).Decode(&request)
			if request.MaxOutputTokens > pipelineReplyTokens || request.Schema == nil {
				t.Error("reply not bounded or not structured", request.MaxOutputTokens)
			}
			last := request.Messages[len(request.Messages)-1]
			if len(heardBy) == 0 && (last.Role != "user" || last.Content[0].Text != "hello there") {
				t.Error("the reply answered before hearing the turn", last)
			}
			heardBy = append(heardBy, last.Content[0].Text)
			turn := turns[0]
			turns = turns[1:]
			_ = json.NewEncoder(w).Encode(map[string]any{"text": "", "object": json.RawMessage(turn), "usage": map[string]int{"inputTokens": 900, "outputTokens": 40}})
		case "/v1/models/speech":
			var speech modelruntime.SpeechRequest
			_ = json.NewDecoder(r.Body).Decode(&speech)
			if speech.Model != "elevenlabs/eleven_flash_v2_5" || speech.Voice != "misty-voice" {
				t.Error("speech request", speech.Model, speech.Voice)
			}
			_ = json.NewEncoder(w).Encode(map[string]string{"audio": base64.StdEncoding.EncodeToString(make([]byte, 30000))})
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(server.Close)
	return modelruntime.New(server.URL, []byte(strings.Repeat("s", 32)), server.Client())
}

func nextEvent(t *testing.T, p *VoicePipeline, kind string) VoiceRealtimeEvent {
	t.Helper()
	deadline := time.After(5 * time.Second)
	for {
		events := make(chan VoiceRealtimeEvent, 1)
		go func() {
			if e, err := p.Read(); err == nil {
				events <- e
			}
		}()
		select {
		case e := <-events:
			if e.Type == "error" {
				t.Fatal("pipeline error", e.Code)
			}
			if e.Type == kind {
				return e
			}
		case <-deadline:
			t.Fatal("no event", kind)
		}
	}
}

func TestVoicePipelineSpeaksTheRealtimeProtocol(t *testing.T) {
	t.Setenv("ELEVENLABS_API_KEY", "elevenlabs-test-key")
	runtime := fakeRuntime(t, `{"say":"Hi! How can I help?","tool":"","instruction":""}`, `{"say":"","tool":"start_task","instruction":"Book a table"}`)
	p, err := NewVoicePipeline(context.Background(), runtime, "openai/gpt-6-luna", "elevenlabs/eleven_flash_v2_5", "misty-voice")
	if err != nil {
		t.Fatal(err)
	}
	defer p.Close()
	p.listenEndpoint = fakeListener(t, "hello there")

	if err = p.Send(VoiceConversationConfig("[]")); err != nil {
		t.Fatal(err)
	}
	ack := nextEvent(t, p, "session-updated")
	if !VoiceRealtimeManualSession(ack.Raw) || !VoiceRealtimeOutputLimit(ack.Raw, VoiceConversationOutputTokens) {
		t.Fatal("session acknowledgement the voice session would reject", string(ack.Raw))
	}

	// One tenth of a second of speech is 4,800 bytes at 24 kHz.
	if err = p.Send(map[string]string{"type": "input-audio-append", "audio": base64.StdEncoding.EncodeToString(make([]byte, 4800))}); err != nil {
		t.Fatal(err)
	}
	// The session asks for the reply right after the commit, as with Realtime.
	if err = p.Send(map[string]string{"type": "input-audio-commit"}); err != nil {
		t.Fatal(err)
	}
	if err = p.Send(map[string]string{"type": "response-create"}); err != nil {
		t.Fatal(err)
	}
	heard := nextEvent(t, p, "input-transcription-completed")
	if usage, err := RealtimeTranscriptionUsage(heard.Raw); heard.Transcript != "hello there" || err != nil || usage["stt_ms"] != 100 {
		t.Fatal("transcript or its usage", heard.Transcript, usage, err)
	}
	spoken := nextEvent(t, p, "audio-transcript-delta")
	audio := nextEvent(t, p, "audio-delta")
	done := nextEvent(t, p, "response-done")
	usage, err := RealtimeResponseUsage(done.Raw)
	if spoken.Delta != "Hi! How can I help?" || audio.Delta == "" || done.Status != "completed" || err != nil || usage["tts_characters"] != 19 || usage["input_tokens"] != 900 {
		t.Fatal("spoken reply", spoken.Delta, done.Status, usage, err)
	}

	if err = p.Send(map[string]string{"type": "response-create"}); err != nil {
		t.Fatal(err)
	}
	call := nextEvent(t, p, "function-call-arguments-done")
	if call.Name != "start_task" || call.Arguments != `{"instruction":"Book a table"}` || call.CallID == "" {
		t.Fatal("tool call", call)
	}
	if done = nextEvent(t, p, "response-done"); done.Status != "completed" {
		t.Fatal("tool turn did not complete", done.Status)
	}
}

func TestSplitSentencesForStreamedSpeech(t *testing.T) {
	for _, tc := range []struct {
		text string
		want []string
	}{
		{"Hi! How can I help?", []string{"Hi! How can I help?"}},
		{"Open Settings first. Then tap the gear in the corner. Done!", []string{"Open Settings first.", "Then tap the gear in the corner. Done!"}},
		{"The file is 2.5 MB. It uploads in seconds.", []string{"The file is 2.5 MB.", "It uploads in seconds."}},
		{"你好！我可以看到你的屏幕。你打开了邮箱。", []string{"你好！我可以看到你的屏幕。", "你打开了邮箱。"}},
		{"Sure", []string{"Sure"}},
	} {
		got := splitSentences(tc.text)
		if strings.Join(got, "|") != strings.Join(tc.want, "|") {
			t.Fatalf("%q -> %q, want %q", tc.text, got, tc.want)
		}
	}
}
