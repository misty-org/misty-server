package wire

import (
	"strings"
	"testing"
)

func TestParseOpen(t *testing.T) {
	open, err := ParseOpen([]byte(`{"type":"open","v":1,"space":"sp_1","key":"app:drawio:board","options":{"history":true},"token":"t","client":"misty-sdk/0.3.0"}`))
	if err != nil || open.Key != "app:drawio:board" || open.Token != "t" {
		t.Fatalf("open %+v %v", open, err)
	}
	for _, bad := range []string{
		`not json`, `{"type":"opened","v":1,"space":"s","key":"k"}`, `{"type":"open","v":2,"space":"s","key":"k"}`,
		`{"type":"open","v":1,"space":"","key":"k"}`, `{"type":"open","v":1,"space":"s","key":""}`,
		`{"type":"open","v":1,"space":"s","key":"a\u0007b"}`,
	} {
		if _, err := ParseOpen([]byte(bad)); err == nil {
			t.Errorf("%s was accepted", bad)
		}
	}
	if ValidKey(strings.Repeat("k", MaxKeyBytes+1)) || !ValidKey(strings.Repeat("k", MaxKeyBytes)) || !ValidKey("é") {
		t.Fatal("key length or UTF-8 rule is wrong")
	}
}

func TestFrames(t *testing.T) {
	frame := UpdateFrame([]byte{1, 2, 3})
	kind, payload, err := Kind(frame.Data)
	if err != nil || kind != MessageSync {
		t.Fatalf("kind %d %v", kind, err)
	}
	step, data, err := Sync(payload)
	if err != nil || step != SyncUpdate || string(data) != string([]byte{1, 2, 3}) {
		t.Fatalf("sync %d %v %v", step, data, err)
	}
	if _, _, err := Sync([]byte{5, 0}); err == nil {
		t.Fatal("an unknown sync step was read")
	}
	awareness := AwarenessFrame(EncodeAwareness([]AwarenessEntry{{Client: 4, Clock: 1, State: []byte(`{"a":1}`)}}))
	kind, payload, _ = Kind(awareness.Data)
	inner, err := AwarenessPayload(payload)
	entries, decodeErr := DecodeAwareness(inner)
	if kind != MessageAwareness || err != nil || decodeErr != nil || len(entries) != 1 || entries[0].Client != 4 {
		t.Fatalf("awareness %v %v %v", entries, err, decodeErr)
	}
}

func TestCloseNames(t *testing.T) {
	for code, name := range map[int]string{1000: "normal", 1011: "internal", 4000: "bad_open", 4001: "unauthorized",
		4003: "forbidden", 4004: "not_found", 4005: "bad_frame", 4006: "options_conflict", 4008: "too_large",
		4009: "full", 4010: "moved", 4011: "gone", 4012: "access_changed", 4013: "signed_out", 4014: "reauth"} {
		if CloseName(code) != name {
			t.Errorf("%d is %s, want %s", code, CloseName(code), name)
		}
	}
}
