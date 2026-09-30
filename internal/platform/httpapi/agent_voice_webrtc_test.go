package api

import (
	"testing"
	"time"

	agent "github.com/kannachi323/misty/server/internal/agents"
)

func TestWebRTCVoiceWaitsForMediaPlaybackAndSettlesMeasuredUsage(t *testing.T) {
	f := newVoiceFixture(t, true)
	f.send(t, voiceClientEvent{Type: "audio.commit", AudioBytes: 24000})
	select {
	case e := <-f.provider.sent:
		if e["type"] != "input-audio-commit" {
			t.Fatal(e)
		}
	case <-time.After(time.Second):
		t.Fatal("PTT release did not commit")
	}
	f.transcript()
	if e := f.read(t); e["type"] != "transcript" {
		t.Fatal(e)
	}
	f.respond(t)
	f.provider.events <- agent.VoiceRealtimeEvent{Type: "playback-started", ResponseID: "reply1"}
	f.responseDone("completed")
	if e := f.read(t); e["type"] != "playing" {
		t.Fatal(e)
	}
	// Ping is an ordering barrier: generation is done, playback must stay alive.
	f.send(t, voiceClientEvent{Type: "ping"})
	if e := f.read(t); e["type"] != "pong" {
		t.Fatal("closed before media drained", e)
	}
	f.provider.events <- agent.VoiceRealtimeEvent{Type: "playback-stopped", ResponseID: "reply1"}
	if e := f.read(t); e["type"] != "done" {
		t.Fatal(e)
	}
	if r := <-f.settled; r.Action != "settle" || r.Usage["output_audio_tokens"] != 6 {
		t.Fatal(r)
	}
}

func TestWebRTCRejectsPCMFallbackInsideSubmittedSession(t *testing.T) {
	f := newVoiceFixture(t, true)
	f.send(t, voiceClientEvent{Type: "audio.append", Audio: "AAAAAA=="})
	if e := f.read(t); e["type"] != "error" {
		t.Fatal(e)
	}
	if len(f.provider.sent) != 0 {
		t.Fatal("audio replayed through sideband")
	}
}
