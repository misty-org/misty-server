package api

import (
	"encoding/json"
	"testing"
)

// inVoice puts a test client in a Space's voice channel, as setVoice does once the person may join.
func inVoice(s *RealtimeService, client *RealtimeClient, spaceID, channelID, keyID string) {
	s.Mu.Lock()
	client.voiceSpaceID, client.voiceChannelID, client.voiceKeyID = spaceID, channelID, keyID
	s.Mu.Unlock()
}

func received(t *testing.T, client *RealtimeClient) []map[string]any {
	t.Helper()
	out := []map[string]any{}
	for {
		select {
		case payload := <-client.Send:
			var message map[string]any
			if err := json.Unmarshal(payload, &message); err != nil {
				t.Fatalf("not JSON: %s", payload)
			}
			out = append(out, message)
		default:
			return out
		}
	}
}

// A voice channel holds eight people, each counted once however many computers they joined from; a
// ninth is refused, and someone already in it can always come back.
func TestVoiceChannelHoldsEightPeople(t *testing.T) {
	s := NewRealtimeService(nil, "")
	room := voiceRoom("spc_1", "cnv_general")
	for index := 0; index < maxVoicePeople; index++ {
		user := string(rune('a' + index))
		inVoice(s, testRealtimeClient(s, user, 4), "spc_1", "cnv_general", "key_"+user)
	}
	// The first person on a second computer is still one person.
	inVoice(s, testRealtimeClient(s, "a", 4), "spc_1", "cnv_general", "key_a2")
	// Others in another channel or another Space don't count.
	inVoice(s, testRealtimeClient(s, "z", 4), "spc_1", "cnv_other", "key_z")
	inVoice(s, testRealtimeClient(s, "y", 4), "spc_2", "cnv_general", "key_y")

	if !s.VoiceRoomFull(room, "ninth") {
		t.Fatal("a ninth person got into a voice channel of eight")
	}
	if s.VoiceRoomFull(room, "a") {
		t.Fatal("someone already in the channel was refused")
	}
	if s.VoiceRoomFull(voiceRoom("spc_1", "cnv_other"), "ninth") {
		t.Fatal("a channel of one was full")
	}
	if s.VoiceRoomFull("dm:spc_1", "ninth") {
		t.Fatal("a DM's call is held by LiveKit's own limit, never refused here")
	}
}

func TestVoiceRoomNamesParseOnlyWhole(t *testing.T) {
	cases := []struct {
		room           string
		space, channel string
		ok             bool
	}{
		{"space:spc_1:voice:cnv_2", "spc_1", "cnv_2", true},
		{"dm:spc_1", "", "", false},
		{"space:spc_1", "", "", false},
		{"space::voice:cnv_2", "", "", false},
		{"space:spc 1:voice:cnv_2", "spc 1", "cnv_2", false},
		{"space:spc_1:voice:", "spc_1", "", false},
	}
	for _, c := range cases {
		space, channel, ok := splitVoiceRoom(c.room)
		if ok != c.ok || (ok && (space != c.space || channel != c.channel)) {
			t.Errorf("splitVoiceRoom(%q) = (%q, %q, %v), want (%q, %q, %v)", c.room, space, channel, ok, c.space, c.channel, c.ok)
		}
	}
}

// A joiner's request for the channel's key reaches only the others in that channel, and only once the
// joiner is in it; the sealed key goes back to the joiner's own computer alone, passed on unread.
func TestVoiceKeysPassOnlyWithinTheChannel(t *testing.T) {
	s := NewRealtimeService(nil, "")
	joiner := testRealtimeClient(s, "joiner", 8)
	joinerOther := testRealtimeClient(s, "joiner", 8)
	there := testRealtimeClient(s, "there", 8)
	elsewhere := testRealtimeClient(s, "elsewhere", 8)
	outside := testRealtimeClient(s, "outside", 8)
	inVoice(s, there, "spc_1", "cnv_general", "key_there")
	inVoice(s, elsewhere, "spc_1", "cnv_other", "key_elsewhere")

	request := voiceMessage{Type: "voice.key_request", SpaceID: "spc_1", ChannelID: "cnv_general", KeyID: "key_joiner", PublicKey: "pub"}
	// Not in the channel yet: nobody hears it.
	s.relayVoiceKeyRequest(joiner, request)
	if got := received(t, there); len(got) != 0 {
		t.Fatalf("a request from outside the channel was passed on: %v", got)
	}

	inVoice(s, joiner, "spc_1", "cnv_general", "key_joiner")
	inVoice(s, joinerOther, "spc_1", "cnv_general", "key_joiner_other")
	// A request under another key than the one it joined with is not its own.
	s.relayVoiceKeyRequest(joiner, voiceMessage{Type: "voice.key_request", SpaceID: "spc_1", ChannelID: "cnv_general", KeyID: "key_stolen", PublicKey: "pub"})
	if got := received(t, there); len(got) != 0 {
		t.Fatalf("a request under another key was passed on: %v", got)
	}
	s.relayVoiceKeyRequest(joiner, request)
	got := received(t, there)
	if len(got) != 1 || got[0]["type"] != "voice.key_request" || got[0]["from"] != "joiner" ||
		got[0]["key_id"] != "key_joiner" || got[0]["public_key"] != "pub" {
		t.Fatalf("the channel heard %v", got)
	}
	for name, client := range map[string]*RealtimeClient{"another channel": elsewhere, "someone outside": outside} {
		if got := received(t, client); len(got) != 0 {
			t.Fatalf("%s heard the request: %v", name, got)
		}
	}
	received(t, joinerOther)

	// The sealed key: only from inside the channel, only to the joiner's computer with that key.
	answer := voiceMessage{Type: "voice.key", SpaceID: "spc_1", ChannelID: "cnv_general", ToUserID: "joiner", ToKeyID: "key_joiner", Sealed: "sealed-bytes"}
	s.relayVoiceKey(outside, answer)
	if got := received(t, joiner); len(got) != 0 {
		t.Fatalf("a key from outside the channel reached the joiner: %v", got)
	}
	s.relayVoiceKey(there, answer)
	got = received(t, joiner)
	if len(got) != 1 || got[0]["type"] != "voice.key" || got[0]["from"] != "there" || got[0]["sealed"] != "sealed-bytes" {
		t.Fatalf("the joiner got %v", got)
	}
	if got := received(t, joinerOther); len(got) != 0 {
		t.Fatalf("the joiner's other computer got a key sealed for another: %v", got)
	}

	// Too large to be a sealed key: dropped.
	big := answer
	big.Sealed = string(make([]byte, maxVoiceSealedBytes+1))
	s.relayVoiceKey(there, big)
	if got := received(t, joiner); len(got) != 0 {
		t.Fatalf("an oversized key was passed on: %v", got)
	}
}

// Messages that aren't voice ones are left for the other handlers; voice ones with a bad Space id are
// taken and dropped.
func TestVoiceMessagesAreRecognisedAndChecked(t *testing.T) {
	s := NewRealtimeService(nil, "")
	client := testRealtimeClient(s, "a", 4)
	if s.handleVoiceMessage(client, []byte(`{"type":"typing"}`)) {
		t.Fatal("a typing message was taken as a voice one")
	}
	if !s.handleVoiceMessage(client, []byte(`{"type":"voice.sync","space_id":"../x"}`)) {
		t.Fatal("a voice message was left for others")
	}
	if got := received(t, client); len(got) != 0 {
		t.Fatalf("a bad Space id was answered: %v", got)
	}
}
