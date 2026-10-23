package agent

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"os"
	"strings"
	"testing"
	"time"
)

// Explicitly opt-in: synthetic locally generated PCM only. Normal test runs
// never contact a provider. Output contains metadata, not audio or transcripts.
func TestRealtimeGatewayLiveFixture(t *testing.T) {
	path := os.Getenv("MISTY_REALTIME_PROBE_PCM")
	if path == "" {
		t.Skip("requires explicit synthetic PCM fixture and gateway credential")
	}
	pcm, err := os.ReadFile(path)
	if err != nil || len(pcm) < 7200 || len(pcm) > 240000 {
		t.Fatal("invalid synthetic fixture")
	}
	key := os.Getenv("MISTY_REALTIME_PROBE_KEY")
	if key == "" {
		t.Fatal("explicit gateway credential required")
	}
	a := &SmartLibraryAnalyzer{APIKey: key}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	v, err := a.OpenVoiceRealtime(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer v.Close()
	if err = v.Configure(); err != nil {
		t.Fatal(err)
	}
	usage := RealtimeVoiceUsage{}
	sentAudio, spoken, transcribed := false, false, false
	audioBytes := 0
	hash := sha256.New()
	outputTranscript := ""
	inputExpected := false
	for {
		_ = v.conn.SetReadDeadline(time.Now().Add(20 * time.Second))
		event, err := v.Read()
		if err != nil {
			t.Fatal("provider event read failed")
		}
		switch event.Type {
		case "session-updated":
			if sentAudio || !VoiceRealtimeManualSession(event.Raw) {
				t.Fatal("manual turn control not confirmed")
			}
			sentAudio = true
			if err = v.Send(map[string]string{"type": "input-audio-append", "audio": base64.StdEncoding.EncodeToString(pcm)}); err != nil {
				t.Fatal(err)
			}
			if err = v.Send(map[string]string{"type": "input-audio-commit"}); err != nil {
				t.Fatal(err)
			}
		case "input-transcription-completed":
			transcribed = true
			inputExpected = strings.Contains(strings.ToLower(event.Transcript), "screen")
			u, err := RealtimeTranscriptionUsage(event.Raw)
			if err != nil {
				t.Fatal(err)
			}
			usage.Add(u)
			if err = v.Send(map[string]any{"type": "conversation-item-create", "item": map[string]string{"type": "text-message", "role": "user", "text": "The confirmed Misty reply to read aloud is:\nBlue lantern."}}); err != nil {
				t.Fatal(err)
			}
			if err = v.Send(map[string]any{"type": "response-create", "options": map[string]any{"modalities": []string{"audio"}, "instructions": "Read the confirmed Misty reply verbatim. Do not answer the original question again or add claims."}}); err != nil {
				t.Fatal(err)
			}
			spoken = true
		case "audio-delta":
			if !spoken {
				t.Fatal("unconfirmed speech")
			}
			chunk, err := base64.StdEncoding.DecodeString(event.Delta)
			if err != nil {
				t.Fatal("invalid PCM")
			}
			audioBytes += len(chunk)
			hash.Write(chunk)
		case "audio-transcript-delta":
			outputTranscript += event.Delta
		case "response-done":
			u, err := RealtimeResponseUsage(event.Raw)
			if err != nil {
				t.Fatal(err)
			}
			usage.Add(u)
			if event.Status != "completed" || !transcribed || !inputExpected || audioBytes == 0 || !strings.Contains(strings.ToLower(outputTranscript), "blue lantern") {
				t.Fatal("synthetic voice acceptance failed")
			}
			report := map[string]any{"scope": "synthetic-go-provider-adapter-only", "model": AgentRealtimeModel, "verified_at": time.Now().UTC().Format(time.RFC3339), "input_bytes": len(pcm), "output_bytes": audioBytes, "output_sha256": hex.EncodeToString(hash.Sum(nil)), "usage": usage, "expected_input": inputExpected, "expected_reply": true, "manual_turn_control": true, "no_speech_before_confirmed_reply": true}
			encoded, _ := json.MarshalIndent(report, "", "  ")
			t.Log(string(encoded))
			if out := os.Getenv("MISTY_REALTIME_PROBE_REPORT"); out != "" {
				if err = os.WriteFile(out, append(encoded, '\n'), 0600); err != nil {
					t.Fatal(err)
				}
			}
			return
		case "error":
			t.Fatal("provider rejected synthetic session", event.Code)
		case "speech-started":
			t.Fatal("unexpected VAD")
		}
	}
}
