package api

import (
	"context"
	"encoding/json"
	"regexp"
	"sort"
	"time"
)

const maxVoicePeople = 8

const (
	voiceFramesPerSecond = 20
	maxVoiceSealedBytes  = 512
	maxVoicePublicKey    = 256
)

var voiceIDPattern = regexp.MustCompile(`^[A-Za-z0-9_-]{1,128}$`)

type voiceMessage struct {
	Type      string `json:"type"`
	SpaceID   string `json:"space_id"`
	ChannelID string `json:"channel_id"`
	KeyID     string `json:"key_id"`
	PublicKey string `json:"public_key"`
	ToUserID  string `json:"to_user_id"`
	ToKeyID   string `json:"to_key_id"`
	Sealed    string `json:"sealed"`
}

type voicePerson struct {
	UserID string `json:"user_id"`
}

type voiceChannelPeople struct {
	ChannelID string        `json:"channel_id"`
	People    []voicePerson `json:"people"`
}

func voiceRoom(spaceID, channelID string) string {
	return "space:" + spaceID + ":voice:" + channelID
}

func (s *RealtimeService) handleVoiceMessage(client *RealtimeClient, payload []byte) bool {
	var msg voiceMessage
	if json.Unmarshal(payload, &msg) != nil {
		return false
	}
	switch msg.Type {
	case "voice", "voice.sync", "voice.key_request", "voice.key":
	default:
		return false
	}
	if !voiceIDPattern.MatchString(msg.SpaceID) || !s.allowVoiceFrame(client) {
		return true
	}
	switch msg.Type {
	case "voice":
		s.setVoice(client, msg)
	case "voice.sync":
		s.sendVoiceState(client, msg.SpaceID)
	case "voice.key_request":
		s.relayVoiceKeyRequest(client, msg)
	case "voice.key":
		s.relayVoiceKey(client, msg)
	}
	return true
}

func (s *RealtimeService) allowVoiceFrame(client *RealtimeClient) bool {
	s.Mu.Lock()
	defer s.Mu.Unlock()
	if time.Since(client.voiceWindowAt) >= time.Second {
		client.voiceWindowAt = time.Now()
		client.voiceFrames = 0
	}
	if client.voiceFrames >= voiceFramesPerSecond {
		return false
	}
	client.voiceFrames++
	return true
}

func (s *RealtimeService) setVoice(client *RealtimeClient, msg voiceMessage) {
	channelID := msg.ChannelID
	if channelID != "" {
		if !voiceIDPattern.MatchString(channelID) || !voiceIDPattern.MatchString(msg.KeyID) {
			return
		}
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		allowed, err := s.database.CanJoinCall(ctx, client.UserID, voiceRoom(msg.SpaceID, channelID))
		cancel()
		if err != nil || !allowed {
			return
		}
		if s.VoiceRoomFull(voiceRoom(msg.SpaceID, channelID), client.UserID) {
			s.sendJSON(client, map[string]any{"type": "voice.full", "space_id": msg.SpaceID, "channel_id": channelID})
			return
		}
	}
	s.Mu.Lock()
	previousSpace := client.voiceSpaceID
	if channelID == "" {
		client.voiceSpaceID, client.voiceChannelID, client.voiceKeyID = "", "", ""
	} else {
		client.voiceSpaceID, client.voiceChannelID, client.voiceKeyID = msg.SpaceID, channelID, msg.KeyID
	}
	s.Mu.Unlock()
	if previousSpace != "" && previousSpace != msg.SpaceID {
		s.broadcastVoice(previousSpace, client.UserID)
	}
	s.broadcastVoice(msg.SpaceID, client.UserID)
}

func (s *RealtimeService) leaveVoice(client *RealtimeClient) {
	s.Mu.Lock()
	spaceID := client.voiceSpaceID
	client.voiceSpaceID, client.voiceChannelID, client.voiceKeyID = "", "", ""
	s.Mu.Unlock()
	if spaceID != "" {
		s.broadcastVoice(spaceID, client.UserID)
	}
}

func (s *RealtimeService) voiceChannelsLocked(spaceID string) map[string][]string {
	seen := map[string]map[string]bool{}
	for client := range s.clients {
		if client.voiceSpaceID != spaceID || client.voiceChannelID == "" {
			continue
		}
		if seen[client.voiceChannelID] == nil {
			seen[client.voiceChannelID] = map[string]bool{}
		}
		seen[client.voiceChannelID][client.UserID] = true
	}
	out := map[string][]string{}
	for channelID, users := range seen {
		for userID := range users {
			out[channelID] = append(out[channelID], userID)
		}
		sort.Strings(out[channelID])
	}
	return out
}

func (s *RealtimeService) VoiceRoomFull(room, userID string) bool {
	spaceID, channelID, ok := splitVoiceRoom(room)
	if !ok {
		return false
	}
	s.Mu.RLock()
	people := s.voiceChannelsLocked(spaceID)[channelID]
	s.Mu.RUnlock()
	others := 0
	for _, id := range people {
		if id != userID {
			others++
		}
	}
	return others >= maxVoicePeople
}

func splitVoiceRoom(room string) (spaceID, channelID string, ok bool) {
	const prefix, middle = "space:", ":voice:"
	if len(room) <= len(prefix) || room[:len(prefix)] != prefix {
		return "", "", false
	}
	rest := room[len(prefix):]
	for index := 0; index+len(middle) <= len(rest); index++ {
		if rest[index:index+len(middle)] == middle {
			spaceID, channelID = rest[:index], rest[index+len(middle):]
			return spaceID, channelID, voiceIDPattern.MatchString(spaceID) && voiceIDPattern.MatchString(channelID)
		}
	}
	return "", "", false
}

func (s *RealtimeService) voiceStateFor(ctx context.Context, userID, spaceID string, channels map[string][]string) []voiceChannelPeople {
	ids := make([]string, 0, len(channels))
	for channelID := range channels {
		ids = append(ids, channelID)
	}
	sort.Strings(ids)
	out := []voiceChannelPeople{}
	for _, channelID := range ids {
		allowed, err := s.database.CanJoinCall(ctx, userID, voiceRoom(spaceID, channelID))
		if err != nil || !allowed {
			continue
		}
		people := make([]voicePerson, 0, len(channels[channelID]))
		for _, id := range channels[channelID] {
			people = append(people, voicePerson{UserID: id})
		}
		out = append(out, voiceChannelPeople{ChannelID: channelID, People: people})
	}
	return out
}

func (s *RealtimeService) broadcastVoice(spaceID, actorUserID string) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	members, err := s.database.SpaceMembers(ctx, actorUserID, spaceID)
	if err != nil {
		return
	}
	s.Mu.RLock()
	channels := s.voiceChannelsLocked(spaceID)
	connected := []string{}
	for _, member := range members {
		if len(s.clientsByUser[member.UserID]) > 0 {
			connected = append(connected, member.UserID)
		}
	}
	s.Mu.RUnlock()
	for _, userID := range connected {
		payload, err := json.Marshal(map[string]any{
			"type": "voice", "space_id": spaceID, "channels": s.voiceStateFor(ctx, userID, spaceID, channels),
		})
		if err != nil {
			continue
		}
		s.SendToUsers([]string{userID}, payload)
	}
}

func (s *RealtimeService) sendVoiceState(client *RealtimeClient, spaceID string) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if _, err := s.database.SpaceByID(ctx, client.UserID, spaceID); err != nil {
		return
	}
	s.Mu.RLock()
	channels := s.voiceChannelsLocked(spaceID)
	s.Mu.RUnlock()
	s.sendJSON(client, map[string]any{
		"type": "voice", "space_id": spaceID, "channels": s.voiceStateFor(ctx, client.UserID, spaceID, channels),
	})
}

func (s *RealtimeService) relayVoiceKeyRequest(client *RealtimeClient, msg voiceMessage) {
	if len(msg.PublicKey) == 0 || len(msg.PublicKey) > maxVoicePublicKey {
		return
	}
	s.Mu.RLock()
	inChannel := client.voiceSpaceID == msg.SpaceID && client.voiceChannelID == msg.ChannelID &&
		msg.ChannelID != "" && client.voiceKeyID == msg.KeyID
	targets := []*RealtimeClient{}
	if inChannel {
		for other := range s.clients {
			if other != client && other.voiceSpaceID == msg.SpaceID && other.voiceChannelID == msg.ChannelID {
				targets = append(targets, other)
			}
		}
	}
	s.Mu.RUnlock()
	if len(targets) == 0 {
		return
	}
	payload, err := json.Marshal(map[string]any{
		"type": "voice.key_request", "space_id": msg.SpaceID, "channel_id": msg.ChannelID,
		"from": client.UserID, "key_id": msg.KeyID, "public_key": msg.PublicKey,
	})
	if err != nil {
		return
	}
	for _, target := range targets {
		s.deliver(target, payload)
	}
}

func (s *RealtimeService) relayVoiceKey(client *RealtimeClient, msg voiceMessage) {
	if len(msg.Sealed) == 0 || len(msg.Sealed) > maxVoiceSealedBytes || !voiceIDPattern.MatchString(msg.ToKeyID) {
		return
	}
	s.Mu.RLock()
	inChannel := client.voiceSpaceID == msg.SpaceID && client.voiceChannelID == msg.ChannelID && msg.ChannelID != ""
	targets := []*RealtimeClient{}
	if inChannel {
		for target := range s.clientsByUser[msg.ToUserID] {
			if target.voiceSpaceID == msg.SpaceID && target.voiceChannelID == msg.ChannelID && target.voiceKeyID == msg.ToKeyID {
				targets = append(targets, target)
			}
		}
	}
	s.Mu.RUnlock()
	payload, err := json.Marshal(map[string]any{
		"type": "voice.key", "space_id": msg.SpaceID, "channel_id": msg.ChannelID,
		"from": client.UserID, "sealed": msg.Sealed,
	})
	if err != nil {
		return
	}
	for _, target := range targets {
		s.deliver(target, payload)
	}
}

func (s *RealtimeService) sendJSON(client *RealtimeClient, message map[string]any) {
	payload, err := json.Marshal(message)
	if err == nil {
		s.deliver(client, payload)
	}
}
