package serverenv

import (
	"encoding/base64"
	"strings"
	"testing"
)

func TestCallsAreReachedThroughTheAPIsOwnHostname(t *testing.T) {
	if got := LiveKitURL(" Misty.Example.com "); got != "wss://misty.example.com/v1/calls" {
		t.Errorf("LiveKitURL = %q", got)
	}
}

func TestDefaultsTurnCallsOnOnlyWithMistyCalls(t *testing.T) {
	secret := base64.StdEncoding.EncodeToString([]byte(strings.Repeat("k", 32)))
	env := func(values map[string]string) func(string) string {
		return func(name string) string { return values[name] }
	}
	base := map[string]string{
		APISecretName:       secret,
		"MISTY_HOSTNAME":    "misty.example.com",
		"MISTY_ENVIRONMENT": "production",
	}
	off, err := Defaults(env(base))
	if err != nil {
		t.Fatal(err)
	}
	if off["LIVEKIT_URL"] != "" {
		t.Fatalf("LIVEKIT_URL without MISTY_CALLS = %q", off["LIVEKIT_URL"])
	}
	if off["LIVEKIT_API_KEY"] != LiveKitAPIKey || len(off["LIVEKIT_API_SECRET"]) != 64 {
		t.Fatalf("LiveKit key and secret = %q, %q", off["LIVEKIT_API_KEY"], off["LIVEKIT_API_SECRET"])
	}
	base["MISTY_CALLS"] = "1"
	on, err := Defaults(env(base))
	if err != nil {
		t.Fatal(err)
	}
	if on["LIVEKIT_URL"] != "wss://misty.example.com/v1/calls" {
		t.Fatalf("LIVEKIT_URL = %q", on["LIVEKIT_URL"])
	}
	if on["LIVEKIT_API_SECRET"] != off["LIVEKIT_API_SECRET"] {
		t.Fatal("the LiveKit secret changed with MISTY_CALLS")
	}
}
