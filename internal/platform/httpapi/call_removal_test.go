package api

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"testing"
)

func TestLiveKitRemovesSomeoneFromManyRoomsAtOnce(t *testing.T) {
	h := newLiveKitHarness(t)
	want := []string{}
	for index := range 20 {
		room := fmt.Sprintf("space:s%d:voice:general", index)
		h.fake.setRoom(room, "PA", "PB")
		want = append(want, room+"/user-PA")
	}
	h.fake.setRoom("space:s99:voice:quiet", "PB")

	if err := h.watcher.removeFromRooms(context.Background(), "space:", []string{"user-PA"}); err != nil {
		t.Fatal(err)
	}
	h.fake.mu.Lock()
	defer h.fake.mu.Unlock()
	got := append([]string(nil), h.fake.removed...)
	sort.Strings(got)
	sort.Strings(want)
	if fmt.Sprint(got) != fmt.Sprint(want) {
		t.Fatalf("removed %v, want %v", got, want)
	}
}

func TestLiveKitDeletesOnlyTheDeletedVoiceChannelsRoom(t *testing.T) {
	h := newLiveKitHarness(t)
	h.fake.setRoom("space:s1:voice:general", "PA")
	h.fake.setRoom("space:s1:voice:general2", "PB")

	if err := h.watcher.deleteRoomIfOpen(context.Background(), voiceRoom("s1", "general")); err != nil {
		t.Fatal(err)
	}
	if got := h.fake.takeDeleted(); len(got) != 1 || got[0] != "space:s1:voice:general" {
		t.Fatalf("deleted %v, want [space:s1:voice:general]", got)
	}

	if err := h.watcher.deleteRoomIfOpen(context.Background(), voiceRoom("s1", "empty")); err != nil {
		t.Fatal(err)
	}
	if got := h.fake.takeDeleted(); len(got) != 0 {
		t.Fatalf("deleted %v for a channel with no call", got)
	}
}

func TestLiveKitRemovesWhoLeftADirectMessageFromOnlyItsCall(t *testing.T) {
	h := newLiveKitHarness(t)
	h.fake.setRoom("dm:1", "PA", "PB", "PC")
	h.fake.setRoom("dm:10", "PA", "PB")

	if err := h.watcher.removeFromRoom(context.Background(), "dm:1", []string{"user-PA"}); err != nil {
		t.Fatal(err)
	}
	h.fake.mu.Lock()
	defer h.fake.mu.Unlock()
	if len(h.fake.removed) != 1 || h.fake.removed[0] != "dm:1/user-PA" {
		t.Fatalf("removed %v, want only user-PA from dm:1", h.fake.removed)
	}
}

func TestLiveKitLeavesAMissingDirectCallAlone(t *testing.T) {
	h := newLiveKitHarness(t)
	h.fake.setRoom("dm:10", "PA")

	if err := h.watcher.removeFromRoom(context.Background(), "dm:1", []string{"user-PA"}); err != nil {
		t.Fatal(err)
	}
	if err := h.watcher.deleteRoomIfOpen(context.Background(), "dm:1"); err != nil {
		t.Fatal(err)
	}
	h.fake.mu.Lock()
	defer h.fake.mu.Unlock()
	if len(h.fake.removed) != 0 || len(h.fake.deleted) != 0 {
		t.Fatalf("removed %v and deleted %v for a room with no call", h.fake.removed, h.fake.deleted)
	}
}

func TestLiveKitRoomMissingReadsLiveKitsNotFound(t *testing.T) {
	if !liveKitRoomMissing(fmt.Errorf("list participants: %w", errors.New("livekit answered 404: room not found"))) {
		t.Fatal("a 404 from LiveKit should read as a missing room")
	}
	if liveKitRoomMissing(errors.New("livekit answered 503: unavailable")) || liveKitRoomMissing(nil) {
		t.Fatal("only a 404 reads as a missing room")
	}
}
