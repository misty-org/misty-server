package agent

import (
	"context"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestAgentSpeechMetersGeneratedSamplesAndReturnsPlayableWAV(t *testing.T) {
	pcm := make([]byte, 48000)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/speech-model" || r.Header.Get("ai-model-id") != AgentSpeechModel {
			t.Errorf("unexpected request: %s", r.URL.Path)
		}
		var payload map[string]string
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			t.Error(err)
		}
		if payload["outputFormat"] != "pcm" || payload["voice"] != "alloy" {
			t.Errorf("payload: %v", payload)
		}
		_ = json.NewEncoder(w).Encode(map[string]string{"audio": base64.StdEncoding.EncodeToString(pcm)})
	}))
	defer server.Close()
	t.Setenv("AI_GATEWAY_EMBEDDING_BASE_URL", server.URL)
	analyzer := &SmartLibraryAnalyzer{APIKey: "test", Client: server.Client()}
	audio, mime, usage, err := analyzer.GenerateAgentSpeechWithUsage(context.Background(), "Hello", "alloy")
	if err != nil || mime != "audio/wav" || usage.DurationMS != 1000 || usage.Model != AgentSpeechModel {
		t.Fatalf("%s %+v %v", mime, usage, err)
	}
	if len(audio) != 48044 || string(audio[:4]) != "RIFF" || string(audio[8:12]) != "WAVE" || binary.LittleEndian.Uint32(audio[40:44]) != 48000 {
		t.Fatal("invalid WAV")
	}
}

func TestAgentVoiceFallbackRecordsActualModelAndDuration(t *testing.T) {
	calls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if calls == 1 {
			http.Error(w, "unavailable", 503)
			return
		}
		if r.Header.Get("ai-model-id") != MediaSearchTranscriptionFallbackModel {
			t.Error("wrong fallback")
		}
		_, _ = w.Write([]byte(`{"text":"Hello","language":"en","durationInSeconds":2.5}`))
	}))
	defer server.Close()
	t.Setenv("AI_GATEWAY_EMBEDDING_BASE_URL", server.URL)
	t.Setenv("AGENT_TRANSCRIPTION_MODEL", "openai/gpt-4o-mini-transcribe")
	analyzer := &SmartLibraryAnalyzer{APIKey: "test", Client: server.Client()}
	text, language, usage, err := analyzer.TranscribeAgentVoiceWithUsage(context.Background(), []byte("recording"), "audio/webm", 1000)
	if err != nil || text != "Hello" || language != "en" || usage.Model != MediaSearchTranscriptionFallbackModel || usage.DurationMS != 2500 {
		t.Fatalf("%s %s %+v %v", text, language, usage, err)
	}
	if calls != 2 {
		t.Fatalf("calls: %d", calls)
	}
}

type voiceTransport func(*http.Request) (*http.Response, error)

func (fn voiceTransport) RoundTrip(r *http.Request) (*http.Response, error) { return fn(r) }
func TestAgentVoiceCancellationDoesNotStartFallback(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	calls := 0
	analyzer := &SmartLibraryAnalyzer{APIKey: "test", Client: &http.Client{Transport: voiceTransport(func(r *http.Request) (*http.Response, error) { calls++; cancel(); return nil, context.Canceled })}}
	_, _, _, err := analyzer.TranscribeAgentVoiceWithUsage(ctx, []byte("audio"), "audio/webm", 1000)
	if err == nil || calls != 1 {
		t.Fatalf("calls=%d err=%v", calls, err)
	}
}
