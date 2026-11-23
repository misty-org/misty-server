package api

import (
	"context"
	"errors"
	"fmt"
	"log"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

const (
	callRemovalTimeout        = 10 * time.Second
	callRemovalAccountTimeout = 2 * time.Minute
	callRemovalRetryAfter     = 30 * time.Second
)

type CallAccess interface {
	CanJoinCall(ctx context.Context, userID, room string) (bool, error)
}

type callEnders struct {
	rooms    *LiveKitRoomWatcher
	realtime *RealtimeService
	access   CallAccess
}

var registeredCallEnders atomic.Pointer[callEnders]

var fallbackCallAdmin = sync.OnceValue(NewLiveKitRoomWatcher)

func SetCallRemoval(rooms *LiveKitRoomWatcher, realtime *RealtimeService, access CallAccess) {
	registeredCallEnders.Store(&callEnders{rooms: rooms, realtime: realtime, access: access})
}

func callAdmin() *LiveKitRoomWatcher {
	if enders := registeredCallEnders.Load(); enders != nil && enders.rooms != nil {
		return enders.rooms
	}
	return fallbackCallAdmin()
}

func callAccess() CallAccess {
	if enders := registeredCallEnders.Load(); enders != nil {
		return enders.access
	}
	return nil
}

func clearVoice(spaceID string, userIDs []string) {
	if enders := registeredCallEnders.Load(); enders != nil && enders.realtime != nil {
		enders.realtime.clearVoiceFor(spaceID, userIDs)
	}
}

func tryCallWork(ctx context.Context, what string, work func(context.Context) error) {
	tryCallWorkWithin(ctx, what, callRemovalTimeout, work)
}

func tryCallWorkWithin(ctx context.Context, what string, within time.Duration, work func(context.Context) error) {
	base := context.WithoutCancel(ctx)
	go func() {
		for attempt := 0; ; attempt++ {
			try, cancel := context.WithTimeout(base, within)
			err := work(try)
			cancel()
			if err == nil {
				return
			}
			log.Printf("calls: %s: %v", what, err)
			if attempt > 0 {
				return
			}
			time.Sleep(callRemovalRetryAfter)
		}
	}()
}

func endSpaceCallsFor(ctx context.Context, spaceID string, userIDs []string) {
	if spaceID == "" || len(userIDs) == 0 {
		return
	}
	ids := append([]string(nil), userIDs...)
	clearVoice(spaceID, ids)
	rooms := callAdmin()
	if rooms == nil {
		return
	}
	tryCallWork(ctx, "end Space calls for removed members", func(work context.Context) error {
		return rooms.removeFromRooms(work, "space:"+spaceID+":voice:", ids)
	})
}

func (s *SpacesService) endSpaceCallsFor(ctx context.Context, spaceID string, userIDs []string) {
	endSpaceCallsFor(ctx, spaceID, userIDs)
}

func endAllCallsFor(ctx context.Context, userID string) {
	rooms := callAdmin()
	if rooms == nil || userID == "" {
		return
	}
	tryCallWorkWithin(ctx, "end the calls of a closed account", callRemovalAccountTimeout, func(work context.Context) error {
		return errors.Join(
			rooms.removeFromRooms(work, "space:", []string{userID}),
			rooms.removeFromRooms(work, "dm:", []string{userID}),
		)
	})
}

func recheckSpaceCalls(ctx context.Context, spaceID string) {
	rooms, access := callAdmin(), callAccess()
	if rooms == nil || access == nil || spaceID == "" {
		return
	}
	tryCallWork(ctx, "recheck who may stay in a Space's calls", func(work context.Context) error {
		removed, err := rooms.removeUnallowed(work, "space:"+spaceID+":voice:", access)
		if len(removed) > 0 {
			clearVoice(spaceID, removed)
		}
		return err
	})
}

func (s *SpacesService) recheckSpaceCalls(ctx context.Context, spaceID string) {
	recheckSpaceCalls(ctx, spaceID)
}

func (w *LiveKitRoomWatcher) removeUnallowed(ctx context.Context, prefix string, access CallAccess) ([]string, error) {
	if w == nil || access == nil {
		return nil, nil
	}
	rooms, err := w.roomsWithPrefix(ctx, prefix)
	if err != nil {
		return nil, err
	}
	var mu sync.Mutex
	var removed []string
	failed := eachCallRoom(ctx, rooms, func(ctx context.Context, room string) error {
		present, err := w.identitiesIn(ctx, room)
		if err != nil {
			return fmt.Errorf("list participants of %q: %w", room, err)
		}
		var failed error
		for identity := range present {
			allowed, err := access.CanJoinCall(ctx, identity, room)
			if err != nil {
				failed = fmt.Errorf("check %q in %q: %w", identity, room, err)
				continue
			}
			if allowed {
				continue
			}
			if err := w.dropFromRoom(ctx, room, identity); err != nil {
				failed = err
				continue
			}
			mu.Lock()
			removed = append(removed, identity)
			mu.Unlock()
		}
		return failed
	})
	return removed, failed
}

func (w *LiveKitRoomWatcher) checkJoined(event liveKitEvent) {
	access := callAccess()
	if w == nil || access == nil || event.Room == nil || event.Participant == nil {
		return
	}
	room, identity := event.Room.Name, event.Participant.Identity
	if room == "" || identity == "" {
		return
	}
	go func() {
		work, cancel := context.WithTimeout(context.Background(), callRemovalTimeout)
		defer cancel()
		allowed, err := access.CanJoinCall(work, identity, room)
		if err != nil {
			log.Printf("calls: check %q joining %q: %v", identity, room, err)
			return
		}
		if allowed {
			return
		}
		if err := w.dropFromRoom(work, room, identity); err != nil {
			log.Printf("calls: remove %q, who may not be in %q: %v", identity, room, err)
			return
		}
		if spaceID, ok := strings.CutPrefix(room, "space:"); ok {
			if at := strings.Index(spaceID, ":voice:"); at > 0 {
				clearVoice(spaceID[:at], []string{identity})
			}
		}
	}()
}

func endVoiceChannelCall(ctx context.Context, spaceID, channelID string) {
	rooms := callAdmin()
	if rooms == nil || spaceID == "" || channelID == "" {
		return
	}
	tryCallWork(ctx, "end a deleted voice channel's call", func(work context.Context) error {
		return rooms.deleteRoomIfOpen(work, voiceRoom(spaceID, channelID))
	})
}

func endDirectCallsBetween(ctx context.Context, userID, otherID string) {
	rooms := callAdmin()
	if rooms == nil || userID == "" || otherID == "" || userID == otherID {
		return
	}
	tryCallWork(ctx, "end a direct call after unfriending", func(work context.Context) error {
		return rooms.endCallsBetween(work, "dm:", []string{userID, otherID})
	})
}

func endDirectCallFor(ctx context.Context, dmID, userID string) {
	rooms := callAdmin()
	if rooms == nil || dmID == "" || userID == "" {
		return
	}
	tryCallWork(ctx, "end the call of someone who left a direct message", func(work context.Context) error {
		return rooms.removeFromRoom(work, "dm:"+dmID, []string{userID})
	})
}
