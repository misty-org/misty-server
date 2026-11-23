package api

import (
	"context"
	"fmt"
	"log"
	"sort"
	"strings"
	"sync"
	"time"
)

const (
	callRoomTimeout  = 5 * time.Second
	callRoomParallel = 8
)

func eachCallRoom(ctx context.Context, rooms []string, work func(context.Context, string) error) error {
	var (
		mu     sync.Mutex
		failed error
		wg     sync.WaitGroup
	)
	slots := make(chan struct{}, callRoomParallel)
	for _, room := range rooms {
		select {
		case slots <- struct{}{}:
		case <-ctx.Done():
			wg.Wait()
			return ctx.Err()
		}
		wg.Add(1)
		go func() {
			defer wg.Done()
			defer func() { <-slots }()
			roomCtx, cancel := context.WithTimeout(ctx, callRoomTimeout)
			defer cancel()
			if err := work(roomCtx, room); err != nil {
				mu.Lock()
				failed = err
				mu.Unlock()
			}
		}()
	}
	wg.Wait()
	return failed
}

func (w *LiveKitRoomWatcher) identitiesIn(ctx context.Context, room string) (map[string]bool, error) {
	var participants struct {
		Participants []liveKitParticipant `json:"participants"`
	}
	if err := w.call(ctx, "ListParticipants", room, map[string]any{"room": room}, &participants); err != nil {
		return nil, err
	}
	out := make(map[string]bool, len(participants.Participants))
	for _, person := range participants.Participants {
		if person.Identity != "" {
			out[person.Identity] = true
		}
	}
	return out, nil
}

func (w *LiveKitRoomWatcher) roomsWithPrefix(ctx context.Context, prefix string) ([]string, error) {
	var listed struct {
		Rooms []liveKitRoom `json:"rooms"`
	}
	if err := w.call(ctx, "ListRooms", "", map[string]any{}, &listed); err != nil {
		return nil, fmt.Errorf("list rooms: %w", err)
	}
	names := []string{}
	for _, room := range listed.Rooms {
		if strings.HasPrefix(room.Name, prefix) {
			names = append(names, room.Name)
		}
	}
	return names, nil
}

func (w *LiveKitRoomWatcher) removeFromRooms(ctx context.Context, prefix string, identities []string) error {
	if w == nil || len(identities) == 0 {
		return nil
	}
	rooms, err := w.roomsWithPrefix(ctx, prefix)
	if err != nil {
		return err
	}
	return eachCallRoom(ctx, rooms, func(ctx context.Context, room string) error {
		present, err := w.identitiesIn(ctx, room)
		if err != nil {
			return fmt.Errorf("list participants of %q: %w", room, err)
		}
		var failed error
		for _, identity := range identities {
			if !present[identity] {
				continue
			}
			if err := w.dropFromRoom(ctx, room, identity); err != nil {
				failed = err
			}
		}
		return failed
	})
}

func (w *LiveKitRoomWatcher) dropFromRoom(ctx context.Context, room, identity string) error {
	reason := "space"
	if strings.HasPrefix(room, "dm:") {
		reason = "direct"
	}
	if err := w.call(ctx, "UpdateParticipant", room, map[string]any{
		"room": room, "identity": identity,
		"attributes": map[string]string{callRemovedAttribute: reason},
	}, nil); err != nil {
		log.Printf("calls: mark %q as removed from %q: %v", identity, room, err)
	}
	if err := w.call(ctx, "RemoveParticipant", room,
		map[string]any{"room": room, "identity": identity}, nil); err != nil {
		return fmt.Errorf("remove %q from %q: %w", identity, room, err)
	}
	return nil
}

func (w *LiveKitRoomWatcher) endCallsBetween(ctx context.Context, prefix string, people []string) error {
	if w == nil || len(people) < 2 {
		return nil
	}
	rooms, err := w.roomsWithPrefix(ctx, prefix)
	if err != nil {
		return err
	}
	return eachCallRoom(ctx, rooms, func(ctx context.Context, room string) error {
		present, err := w.identitiesIn(ctx, room)
		if err != nil {
			return fmt.Errorf("list participants of %q: %w", room, err)
		}
		for _, person := range people {
			if !present[person] {
				return nil
			}
		}
		if err := w.call(ctx, "DeleteRoom", room, map[string]any{"room": room}, nil); err != nil {
			return fmt.Errorf("delete room %q: %w", room, err)
		}
		return nil
	})
}

func (w *LiveKitRoomWatcher) deleteRoomIfOpen(ctx context.Context, room string) error {
	if w == nil || room == "" {
		return nil
	}
	present, err := w.identitiesIn(ctx, room)
	if liveKitRoomMissing(err) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("list participants of %q: %w", room, err)
	}
	if len(present) == 0 {
		return nil
	}
	if err := w.call(ctx, "DeleteRoom", room, map[string]any{"room": room}, nil); err != nil && !liveKitRoomMissing(err) {
		return fmt.Errorf("delete room %q: %w", room, err)
	}
	return nil
}

func liveKitRoomMissing(err error) bool {
	return err != nil && strings.Contains(err.Error(), "livekit answered 404")
}

func (w *LiveKitRoomWatcher) prepareCallRoom(ctx context.Context, room, userID string, most int) ([]string, bool, error) {
	if w == nil {
		return nil, false, nil
	}
	if err := w.call(ctx, "CreateRoom", room, map[string]any{
		"name": room, "max_participants": most, "empty_timeout": 30, "departure_timeout": 20,
	}, nil); err != nil {
		return nil, false, fmt.Errorf("create room %q: %w", room, err)
	}
	present, err := w.identitiesIn(ctx, room)
	if err != nil {
		return nil, false, fmt.Errorf("list participants of %q: %w", room, err)
	}
	others := []string{}
	for identity := range present {
		if identity != userID {
			others = append(others, identity)
		}
	}
	sort.Strings(others)
	return others, len(others) >= most, nil
}

func (w *LiveKitRoomWatcher) removeFromRoom(ctx context.Context, room string, identities []string) error {
	if w == nil || room == "" || len(identities) == 0 {
		return nil
	}
	present, err := w.identitiesIn(ctx, room)
	if liveKitRoomMissing(err) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("list participants of %q: %w", room, err)
	}
	var failed error
	for _, identity := range identities {
		if !present[identity] {
			continue
		}
		if err := w.dropFromRoom(ctx, room, identity); err != nil && !liveKitRoomMissing(err) {
			failed = err
		}
	}
	return failed
}
