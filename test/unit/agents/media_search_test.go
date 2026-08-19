package agent

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	. "github.com/kannachi323/misty/server/internal/agents"
)

func TestGenerateAgentSpeechUsesGatewayAndDecodesAudio(t *testing.T) {
	expected := []byte("misty speech")
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/speech-model" || r.Method != http.MethodPost {
			t.Fatalf("request=%s %s", r.Method, r.URL.Path)
		}
		if r.Header.Get("Authorization") != "Bearer test-key" || r.Header.Get("ai-model-id") != AgentSpeechModel {
			t.Fatalf("headers=%v", r.Header)
		}
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatal(err)
		}
		if body["text"] != "hello" || body["voice"] != "alloy" || body["outputFormat"] != "mp3" {
			t.Fatalf("body=%v", body)
		}
		_ = json.NewEncoder(w).Encode(map[string]string{"audio": base64.StdEncoding.EncodeToString(expected)})
	}))
	defer server.Close()

	analyzer := &SmartLibraryAnalyzer{APIKey: "test-key", BaseURL: server.URL, Client: server.Client()}
	audio, contentType, err := analyzer.GenerateAgentSpeech(context.Background(), "hello", "alloy")
	if err != nil {
		t.Fatal(err)
	}
	if string(audio) != string(expected) || contentType != "audio/mpeg" {
		t.Fatalf("audio=%q contentType=%q", audio, contentType)
	}
}

func TestAgentVoiceRejectsInvalidInputBeforeProviderCall(t *testing.T) {
	analyzer := &SmartLibraryAnalyzer{}
	if _, _, _, err := analyzer.TranscribeAgentVoice(context.Background(), nil, "audio/webm", 1_000); err == nil {
		t.Fatal("empty recording accepted")
	}
	if _, _, _, err := analyzer.TranscribeAgentVoice(context.Background(), []byte("x"), "audio/webm", 60_001); err == nil {
		t.Fatal("long recording accepted")
	}
	if _, _, err := analyzer.GenerateAgentSpeech(context.Background(), "", "alloy"); err == nil {
		t.Fatal("empty speech accepted")
	}
}

func TestTranscribeAgentVoiceReturnsDetectedLanguageAndDuration(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"text":              "hello there",
			"language":          "en",
			"durationInSeconds": 1.25,
			"segments": []map[string]any{
				{"startSecond": 0, "endSecond": 1.25, "text": "hello there"},
			},
		})
	}))
	defer server.Close()

	analyzer := &SmartLibraryAnalyzer{APIKey: "test-key", BaseURL: server.URL, Client: server.Client()}
	transcript, language, durationMS, err := analyzer.TranscribeAgentVoice(context.Background(), []byte("voice"), "audio/webm", 2_000)
	if err != nil {
		t.Fatal(err)
	}
	if transcript != "hello there" || language != "en" || durationMS != 1_250 {
		t.Fatalf("transcript=%q language=%q duration=%d", transcript, language, durationMS)
	}
}

func TestTranscribeMediaReturnsTimestampedSegments(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/transcription-model" {
			t.Fatalf("path=%s", r.URL.Path)
		}
		if r.Header.Get("Authorization") != "Bearer test-key" {
			t.Fatal("missing authorization")
		}
		if r.Header.Get("ai-model-id") != MediaSearchTranscriptionModel || r.Header.Get("ai-transcription-model-specification-version") != "4" {
			t.Fatalf("headers=%v", r.Header)
		}
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatal(err)
		}
		if body["mediaType"] != "audio/mpeg" || body["audio"] == "" {
			t.Fatalf("body=%v", body)
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"text": "hello world", "segments": []map[string]any{{"startSecond": 1.25, "endSecond": 2.5, "text": "hello world"}}})
	}))
	defer server.Close()
	analyzer := &SmartLibraryAnalyzer{APIKey: "test-key", BaseURL: server.URL, Client: server.Client()}
	segments, _, err := analyzer.TranscribeMedia(context.Background(), []byte("fake mp3"), "audio/mpeg", 30_000)
	if err != nil {
		t.Fatal(err)
	}
	if len(segments) != 1 || segments[0].StartMS != 1250 || segments[0].EndMS != 2500 || segments[0].Text != "hello world" {
		t.Fatalf("segments=%+v", segments)
	}
}

func TestCoalesceTranscriptSegmentsPreservesUsefulTimestamps(t *testing.T) {
	input := []MediaTranscriptSegment{{StartMS: 100, EndMS: 400, Text: "hello"}, {StartMS: 410, EndMS: 900, Text: "there"}, {StartMS: 920, EndMS: 3_100, Text: "friend."}, {StartMS: 3_150, EndMS: 3_600, Text: "next"}}
	got := TestingCoalesceTranscriptSegments(input)
	if len(got) != 2 || got[0].StartMS != 100 || got[0].EndMS != 3100 || got[0].Text != "hello there friend." || got[1].StartMS != 3150 {
		t.Fatalf("got=%+v", got)
	}
}
func TestTranscribeMediaRejectsOversizedAndLongChunks(t *testing.T) {
	analyzer := &SmartLibraryAnalyzer{}
	if _, _, err := analyzer.TranscribeMedia(context.Background(), make([]byte, (2<<20)+1), "audio/mpeg", 30_000); err == nil {
		t.Fatal("oversized audio accepted")
	}
	if _, _, err := analyzer.TranscribeMedia(context.Background(), []byte("x"), "audio/mpeg", 35_001); err == nil {
		t.Fatal("long chunk accepted")
	}
}
