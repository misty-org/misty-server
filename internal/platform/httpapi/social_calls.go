package api

import (
	"encoding/json"
	"net/http"
	"net/url"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/golang-jwt/jwt/v5"
)

// Calls in DMs: ringing, LiveKit room tokens, and each computer's public key for sealing a call's
// end-to-end key. The call itself and its key never pass through this server.

// DirectCall rings or stops ringing a DM's other people, or says the caller was declined:
// {state: "ringing"|"ended"|"declined", video}. Only
// the call's room and who is calling travel; the call itself goes through LiveKit.
func (s *SocialService) DirectCall() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		userID, ok := authenticatedUser(w, r, s.database)
		if !ok {
			return
		}
		var body struct {
			State string `json:"state"`
			Video bool   `json:"video"`
			// SealedKeys is the call's end-to-end key sealed for each computer (by key id), which only
			// that computer can open. It passes through unread.
			SealedKeys map[string]string `json:"sealed_keys"`
			// SenderKeyID is the caller's computer's key id, so the others know whose public key sealed it.
			SenderKeyID string `json:"sender_key_id"`
		}
		if decodeJSON(w, r, &body) != nil {
			return
		}
		// "declined" tells the caller one person said no; the ring goes on for anyone else.
		if body.State != "ringing" && body.State != "ended" && body.State != "declined" ||
			len(body.SealedKeys) > 100 || len(body.SenderKeyID) > 64 {
			writeJSON(w, http.StatusBadRequest, map[string]string{"code": "invalid_request"})
			return
		}
		for keyID, sealed := range body.SealedKeys {
			if len(keyID) > 64 || len(sealed) > 512 {
				writeJSON(w, http.StatusBadRequest, map[string]string{"code": "invalid_request"})
				return
			}
		}
		dmID := chi.URLParam(r, "dmID")
		allowed, err := s.database.CanJoinCall(r.Context(), userID, "dm:"+dmID)
		if err != nil {
			writeSpaceError(w, err)
			return
		}
		if !allowed {
			writeJSON(w, http.StatusForbidden, map[string]string{"code": "forbidden"})
			return
		}
		members, err := s.database.DirectMemberIDs(r.Context(), userID, dmID)
		if err != nil {
			writeSpaceError(w, err)
			return
		}
		others := []string{}
		for _, id := range members {
			if id != userID {
				others = append(others, id)
			}
		}
		payload, _ := json.Marshal(map[string]any{"type": "call", "dm_id": dmID, "room": "dm:" + dmID, "from": userID,
			"state": body.State, "video": body.Video, "sealed_keys": body.SealedKeys, "sender_key_id": body.SenderKeyID})
		if s.realtime != nil {
			s.realtime.SendToUsers(others, payload)
		}
		w.WriteHeader(http.StatusNoContent)
	}
}

// CallConfig tells the app whether calls are on, and where LiveKit is.
func (s *SocialService) CallConfig() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if _, ok := authenticatedUser(w, r, s.database); !ok {
			return
		}
		enabled := s.liveKitURL != "" && s.liveKitKey != "" && s.liveKitSecret != ""
		out := map[string]any{"enabled": enabled, "max_people": 8}
		if enabled {
			out["url"] = s.liveKitURL
		}
		writeJSON(w, http.StatusOK, out)
	}
}

// CallToken mints a LiveKit token for one room, after checking the caller may join it by the
// room's kind ("dm:<dm id>" today; "space:<id>:voice:<channel>" later). Media and the call's
// end-to-end key never pass through this server.
func (s *SocialService) CallToken() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		userID, ok := authenticatedUser(w, r, s.database)
		if !ok {
			return
		}
		if s.liveKitKey == "" || s.liveKitSecret == "" || s.liveKitURL == "" {
			writeJSON(w, http.StatusServiceUnavailable, map[string]string{"code": "calls_unavailable"})
			return
		}
		// The room's name has colons (`dm:<id>`, `space:<id>:voice:<channel>`), which the app
		// escapes in the path; the router hands the segment over still escaped.
		room, err := url.PathUnescape(chi.URLParam(r, "room"))
		if err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"code": "invalid_room"})
			return
		}
		allowed, err := s.database.CanJoinCall(r.Context(), userID, room)
		if err != nil {
			writeSpaceError(w, err)
			return
		}
		if !allowed {
			writeJSON(w, http.StatusForbidden, map[string]string{"code": "forbidden"})
			return
		}
		// A voice channel holds at most 8 people; LiveKit's own room limit backs this for every room.
		if s.realtime != nil && s.realtime.VoiceRoomFull(room, userID) {
			writeJSON(w, http.StatusConflict, map[string]string{"code": "room_full"})
			return
		}
		now := time.Now()
		claims := jwt.MapClaims{
			"iss": s.liveKitKey,
			"sub": userID,
			"nbf": now.Add(-10 * time.Second).Unix(),
			"exp": now.Add(10 * time.Minute).Unix(),
			// canUpdateOwnMetadata lets each computer put its public call key on itself (attributes),
			// which is how the people in a call seal the call's key to one another.
			"video": map[string]any{
				"room": room, "roomJoin": true, "canPublish": true, "canSubscribe": true, "canPublishData": true,
				"canUpdateOwnMetadata": true,
			},
		}
		token, err := jwt.NewWithClaims(jwt.SigningMethodHS256, claims).SignedString([]byte(s.liveKitSecret))
		if err != nil {
			writeSpaceError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"token": token, "url": s.liveKitURL, "room": room})
	}
}

// CallKey saves this computer's public key for calls (PUT /me/call-keys/{keyID} {public_key}).
func (s *SocialService) CallKey() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		userID, ok := authenticatedUser(w, r, s.database)
		if !ok {
			return
		}
		var body struct {
			PublicKey string `json:"public_key"`
		}
		if decodeJSON(w, r, &body) != nil {
			return
		}
		if err := s.database.PutCallKey(r.Context(), userID, chi.URLParam(r, "keyID"), body.PublicKey); err != nil {
			writeSpaceError(w, err)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}
}

// DirectCallKeys lists the public call keys of everyone in a DM, to seal a call's key for them.
func (s *SocialService) DirectCallKeys() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		userID, ok := authenticatedUser(w, r, s.database)
		if !ok {
			return
		}
		keys, err := s.database.DirectCallKeys(r.Context(), userID, chi.URLParam(r, "dmID"))
		if err != nil {
			writeSpaceError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"keys": keys})
	}
}
