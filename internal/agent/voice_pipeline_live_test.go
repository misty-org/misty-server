package agent

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/kannachi323/misty/server/internal/modelruntime"
)

// A round trip through the real providers: the runtime speaks a sentence with
// ElevenLabs Flash, then Misty's listener transcribes that audio with Scribe.
// It costs a fraction of a cent, so it runs only when asked:
//
//	MISTY_VOICE_LIVE=1 ELEVENLABS_API_KEY=… MISTY_AGENT_RUNTIME_URL=http://127.0.0.1:… \
//	MISTY_AGENT_RUNTIME_CONTROL_SECRET=… go test ./internal/agent -run VoicePipelineLive
func TestVoicePipelineLiveElevenLabsRoundTrip(t *testing.T) {
	if os.Getenv("MISTY_VOICE_LIVE") != "1" {
		t.Skip("set MISTY_VOICE_LIVE=1 with ElevenLabs and runtime credentials")
	}
	models, err := modelruntime.FromEnv()
	if err != nil || !models.Enabled() {
		t.Fatal("agent runtime is not configured", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	model := os.Getenv("MISTY_VOICE_TTS_MODEL")
	if model == "" {
		model = "elevenlabs/eleven_flash_v2_5"
	}
	pcm, err := models.Speak(ctx, modelruntime.SpeechRequest{Route: modelruntime.Instance(), Model: model, Text: "Hello! I'm Misty, and I'm happy to help you today.", Voice: os.Getenv("MISTY_VOICE_ID")})
	if err != nil || len(pcm) < 24000 {
		t.Fatalf("speech: %d bytes, %v", len(pcm), err)
	}
	t.Logf("speech: %.1f seconds of 24 kHz audio", float64(len(pcm))/48000)
	listener, err := openElevenLabsListener(ctx, "")
	if err != nil {
		t.Fatal(err)
	}
	// Stream at speaking pace in tenth-of-a-second chunks, as the app does.
	for start := 0; start < len(pcm); start += 4800 {
		if err = listener.Write(pcm[start:min(start+4800, len(pcm))]); err != nil {
			t.Fatal(err)
		}
		time.Sleep(100 * time.Millisecond)
	}
	text, err := listener.Finish(ctx)
	t.Logf("transcript: %q", text)
	if err != nil || !strings.Contains(strings.ToLower(text), "misty") {
		t.Fatal("round trip lost the sentence", err)
	}
}
