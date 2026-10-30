package api

import (
	"strings"
	"testing"
	"time"
)

func TestLiveTicketsAreSignedBoundAndExpire(t *testing.T) {
	secret := []byte(strings.Repeat("s", 32))
	now := time.Unix(1_800_000_000, 0)
	claims := LiveTicketClaims{SessionID: "session-1", User: "user-1", Device: "device-1", Channels: []string{"mic", "system"}, MaxMS: 900_000, Model: "soniox/stt-rt-v4", ExpiresAt: now.Add(time.Minute).Unix()}
	ticket, err := signLiveTicket(secret, claims)
	if err != nil || !strings.HasPrefix(ticket, "v1.") {
		t.Fatalf("signLiveTicket() = %q, %v", ticket, err)
	}
	got, err := verifyLiveTicket([][]byte{secret}, ticket, now)
	if err != nil || got.SessionID != "session-1" || got.User != "user-1" || len(got.Channels) != 2 || got.MaxMS != 900_000 {
		t.Fatalf("verifyLiveTicket() = %+v, %v", got, err)
	}
	// The previous secret still verifies during rotation.
	if _, err = verifyLiveTicket([][]byte{[]byte(strings.Repeat("n", 32)), secret}, ticket, now); err != nil {
		t.Fatal("rotation lost the previous secret:", err)
	}
	if _, err = verifyLiveTicket([][]byte{secret}, ticket, now.Add(time.Minute)); err == nil {
		t.Fatal("expired ticket accepted")
	}
	if _, err = verifyLiveTicket([][]byte{[]byte(strings.Repeat("x", 32))}, ticket, now); err == nil {
		t.Fatal("ticket signed with another secret accepted")
	}
	// Changing any claim breaks the signature.
	parts := strings.Split(ticket, ".")
	forged, _ := signLiveTicket([]byte(strings.Repeat("x", 32)), LiveTicketClaims{SessionID: "session-1", User: "someone-else", MaxMS: 1, ExpiresAt: claims.ExpiresAt})
	if _, err = verifyLiveTicket([][]byte{secret}, parts[0]+"."+strings.Split(forged, ".")[1]+"."+parts[2], now); err == nil {
		t.Fatal("swapped claims accepted")
	}
	if _, err = signLiveTicket(nil, claims); err == nil {
		t.Fatal("signed without a secret")
	}
}

func TestLiveChannelsAreMicAndSystemOnce(t *testing.T) {
	if got, ok := liveChannels([]string{"mic", "system"}); !ok || len(got) != 2 {
		t.Fatal("mic and system refused")
	}
	for _, bad := range [][]string{nil, {}, {"mic", "mic"}, {"speaker"}} {
		if _, ok := liveChannels(bad); ok {
			t.Fatalf("accepted %v", bad)
		}
	}
}

func TestLiveRolesHaveDefaultModels(t *testing.T) {
	for _, role := range []string{"live-transcription", "diarized-transcription", "dictation-cleanup"} {
		if model := defaultRoleModel(role); !strings.Contains(model, "/") {
			t.Fatalf("%s default = %q", role, model)
		}
	}
}

// The runtime (server/apps/agent-runtime/test/live-ticket.test.ts) verifies
// this exact ticket, so both sides must keep the same wire format.
const sharedLiveTicketVector = "v1.eyJzaWQiOiJzZXNzaW9uLTEiLCJzdWIiOiJ1c2VyLTEiLCJkZXYiOiJkZXZpY2UtMSIsImNoIjpbIm1pYyIsInN5c3RlbSJdLCJtYXgiOjkwMDAwMCwibW9kZWwiOiJzb25pb3gvc3R0LXJ0LXY0IiwiZXhwIjoxODAwMDAwMDYwfQ.117a28a26f8bf98cdd9d6a49c7037675f6e3c7f2b59d7eb7115f6162436d7417"

func TestLiveTicketWireFormatMatchesTheRuntime(t *testing.T) {
	ticket, err := signLiveTicket([]byte(strings.Repeat("s", 32)), LiveTicketClaims{SessionID: "session-1", User: "user-1", Device: "device-1", Channels: []string{"mic", "system"}, MaxMS: 900_000, Model: "soniox/stt-rt-v4", ExpiresAt: 1_800_000_060})
	if err != nil || ticket != sharedLiveTicketVector {
		t.Fatalf("ticket = %q, %v", ticket, err)
	}
}
