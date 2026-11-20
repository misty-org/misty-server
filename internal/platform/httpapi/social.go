package api

import (
	"encoding/json"
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/golang-jwt/jwt/v5"
	"github.com/misty-org/misty-server/internal/platform/config"
	"github.com/misty-org/misty-server/internal/platform/db"
)

// SocialService serves what belongs to the account rather than a Space: friends, DMs, status,
// one-time notices and call tokens. A DM is a Space of kind 'direct', so its messages go through
// the Space chat routes by its id.
type SocialService struct {
	database *db.Database
	realtime *RealtimeService
	// LiveKit, which carries calls. Without it calls are off and the app shows no call buttons.
	liveKitURL, liveKitKey, liveKitSecret string
}

func NewSocialService(database *db.Database, realtime *RealtimeService) *SocialService {
	return &SocialService{
		database:      database,
		realtime:      realtime,
		liveKitURL:    strings.TrimSpace(config.Getenv("LIVEKIT_URL")),
		liveKitKey:    strings.TrimSpace(config.Getenv("LIVEKIT_API_KEY")),
		liveKitSecret: strings.TrimSpace(config.Getenv("LIVEKIT_API_SECRET")),
	}
}

// Friends lists friends, requests both ways and blocks; their statuses come from Statuses.
func (s *SocialService) Friends() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		userID, ok := authenticatedUser(w, r, s.database)
		if !ok {
			return
		}
		list, err := s.database.Friends(r.Context(), userID)
		if err != nil {
			writeSpaceError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, list)
	}
}

// FriendRequests sends a request by username (POST), or ignores every incoming one (DELETE).
func (s *SocialService) FriendRequests() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		userID, ok := authenticatedUser(w, r, s.database)
		if !ok {
			return
		}
		if r.Method == http.MethodDelete {
			if err := s.database.ClearIncomingFriendRequests(r.Context(), userID); err != nil {
				writeSpaceError(w, err)
				return
			}
			w.WriteHeader(http.StatusNoContent)
			return
		}
		var body struct {
			Username string `json:"username"`
		}
		if decodeJSON(w, r, &body) != nil {
			return
		}
		if err := s.database.SendFriendRequest(r.Context(), userID, body.Username); err != nil {
			writeSpaceError(w, err)
			return
		}
		// The same answer whether or not it reached anyone.
		w.WriteHeader(http.StatusAccepted)
	}
}

// FriendRequest accepts (POST …/accept) or ignores and cancels (DELETE) one request.
func (s *SocialService) FriendRequest() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		userID, ok := authenticatedUser(w, r, s.database)
		if !ok {
			return
		}
		otherID := chi.URLParam(r, "userID")
		var err error
		if r.Method == http.MethodDelete {
			err = s.database.RemoveFriendRequest(r.Context(), userID, otherID)
		} else {
			err = s.database.AcceptFriendRequest(r.Context(), userID, otherID)
			if err == nil {
				s.publishStatus(userID)
				s.publishStatus(otherID)
			}
		}
		if err != nil {
			writeSpaceError(w, err)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}
}

// Friend ends a friendship, which deletes the pair's one-to-one DM for both.
func (s *SocialService) Friend() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		userID, ok := authenticatedUser(w, r, s.database)
		if !ok {
			return
		}
		if err := s.database.Unfriend(r.Context(), userID, chi.URLParam(r, "userID")); err != nil {
			writeSpaceError(w, err)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}
}

// Block blocks (PUT) or unblocks (DELETE) someone.
func (s *SocialService) Block() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		userID, ok := authenticatedUser(w, r, s.database)
		if !ok {
			return
		}
		otherID := chi.URLParam(r, "userID")
		var err error
		if r.Method == http.MethodDelete {
			err = s.database.Unblock(r.Context(), userID, otherID)
		} else {
			err = s.database.Block(r.Context(), userID, otherID)
		}
		if err != nil {
			writeSpaceError(w, err)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}
}

// Privacy reads (GET) or sets (PUT) who may send the caller friend requests.
func (s *SocialService) Privacy() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		userID, ok := authenticatedUser(w, r, s.database)
		if !ok {
			return
		}
		if r.Method == http.MethodGet {
			privacy, err := s.database.Privacy(r.Context(), userID)
			if err != nil {
				writeSpaceError(w, err)
				return
			}
			writeJSON(w, http.StatusOK, privacy)
			return
		}
		var body db.FriendPrivacy
		if decodeJSON(w, r, &body) != nil {
			return
		}
		privacy, err := s.database.SetPrivacy(r.Context(), userID, body)
		if err != nil {
			writeSpaceError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, privacy)
	}
}

// Directs lists the caller's DMs (GET), or opens one (POST): {user_id} finds or starts the
// one-to-one DM with a friend, {user_ids} starts a group with two to nine friends.
func (s *SocialService) Directs() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		userID, ok := authenticatedUser(w, r, s.database)
		if !ok {
			return
		}
		if r.Method == http.MethodGet {
			items, err := s.database.Directs(r.Context(), userID)
			if err != nil {
				writeSpaceError(w, err)
				return
			}
			writeJSON(w, http.StatusOK, map[string]any{"dms": items})
			return
		}
		var body struct {
			UserID  string   `json:"user_id"`
			UserIDs []string `json:"user_ids"`
		}
		if decodeJSON(w, r, &body) != nil {
			return
		}
		var item *db.DirectConversation
		var err error
		if len(body.UserIDs) > 1 {
			item, err = s.database.CreateGroupDirect(r.Context(), userID, body.UserIDs)
		} else {
			target := body.UserID
			if len(body.UserIDs) == 1 {
				target = body.UserIDs[0]
			}
			item, err = s.database.OpenDirect(r.Context(), userID, target)
		}
		if err != nil {
			writeSpaceError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, item)
	}
}

// Direct returns one DM (GET) or deletes a one-to-one DM for both people (DELETE).
func (s *SocialService) Direct() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		userID, ok := authenticatedUser(w, r, s.database)
		if !ok {
			return
		}
		dmID := chi.URLParam(r, "dmID")
		if r.Method == http.MethodDelete {
			if err := s.database.DeleteDirect(r.Context(), userID, dmID); err != nil {
				writeSpaceError(w, err)
				return
			}
			w.WriteHeader(http.StatusNoContent)
			return
		}
		item, err := s.database.Direct(r.Context(), userID, dmID)
		if err != nil {
			writeSpaceError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, item)
	}
}

// DirectMembers adds friends to a DM; adding to a one-to-one starts a new group, returned.
func (s *SocialService) DirectMembers() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		userID, ok := authenticatedUser(w, r, s.database)
		if !ok {
			return
		}
		var body struct {
			UserIDs []string `json:"user_ids"`
		}
		if decodeJSON(w, r, &body) != nil {
			return
		}
		item, err := s.database.AddDirectMembers(r.Context(), userID, chi.URLParam(r, "dmID"), body.UserIDs)
		if err != nil {
			writeSpaceError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, item)
	}
}

// LeaveDirect takes the caller out of a group DM.
func (s *SocialService) LeaveDirect() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		userID, ok := authenticatedUser(w, r, s.database)
		if !ok {
			return
		}
		if err := s.database.LeaveDirect(r.Context(), userID, chi.URLParam(r, "dmID")); err != nil {
			writeSpaceError(w, err)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}
}

// DirectCall rings or stops ringing a DM's other people: {state: "ringing"|"ended", video}. Only
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
		}
		if decodeJSON(w, r, &body) != nil {
			return
		}
		if body.State != "ringing" && body.State != "ended" {
			writeJSON(w, http.StatusBadRequest, map[string]string{"code": "invalid_request"})
			return
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
		payload, _ := json.Marshal(map[string]any{"type": "call", "dm_id": dmID, "room": "dm:" + dmID, "from": userID, "state": body.State, "video": body.Video})
		if s.realtime != nil {
			s.realtime.SendToUsers(others, payload)
		}
		w.WriteHeader(http.StatusNoContent)
	}
}

// Status reads (GET) or sets (PUT) the caller's chosen status and custom line.
func (s *SocialService) Status() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		userID, ok := authenticatedUser(w, r, s.database)
		if !ok {
			return
		}
		if r.Method == http.MethodGet {
			chosen, err := s.database.ChosenStatuses(r.Context(), []string{userID})
			if err != nil {
				writeSpaceError(w, err)
				return
			}
			writeJSON(w, http.StatusOK, chosen[userID])
			return
		}
		var body db.ChosenStatus
		if decodeJSON(w, r, &body) != nil {
			return
		}
		saved, err := s.database.SetChosenStatus(r.Context(), userID, body)
		if err != nil {
			writeSpaceError(w, err)
			return
		}
		s.publishStatus(userID)
		writeJSON(w, http.StatusOK, saved)
	}
}

// Statuses returns how everyone who may see each other with the caller shows right now: friends
// and people sharing a Space. The app takes changes from the realtime "status" message after.
func (s *SocialService) Statuses() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		userID, ok := authenticatedUser(w, r, s.database)
		if !ok {
			return
		}
		audience, err := s.database.StatusAudience(r.Context(), userID)
		if err != nil {
			writeSpaceError(w, err)
			return
		}
		if s.realtime == nil {
			writeJSON(w, http.StatusOK, map[string]any{"statuses": []AccountStatus{}})
			return
		}
		statuses, err := s.realtime.Statuses(r.Context(), audience)
		if err != nil {
			writeSpaceError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"statuses": statuses})
	}
}

// Notices lists the caller's unseen notices (GET), or marks one seen (POST …/{noticeID}/seen).
func (s *SocialService) Notices() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		userID, ok := authenticatedUser(w, r, s.database)
		if !ok {
			return
		}
		if noticeID := chi.URLParam(r, "noticeID"); noticeID != "" {
			if err := s.database.MarkAccountNoticeSeen(r.Context(), userID, noticeID); err != nil {
				writeSpaceError(w, err)
				return
			}
			w.WriteHeader(http.StatusNoContent)
			return
		}
		notices, err := s.database.AccountNotices(r.Context(), userID)
		if err != nil {
			writeSpaceError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"notices": notices})
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
		room := chi.URLParam(r, "room")
		allowed, err := s.database.CanJoinCall(r.Context(), userID, room)
		if err != nil {
			writeSpaceError(w, err)
			return
		}
		if !allowed {
			writeJSON(w, http.StatusForbidden, map[string]string{"code": "forbidden"})
			return
		}
		now := time.Now()
		claims := jwt.MapClaims{
			"iss": s.liveKitKey,
			"sub": userID,
			"nbf": now.Add(-10 * time.Second).Unix(),
			"exp": now.Add(10 * time.Minute).Unix(),
			"video": map[string]any{
				"room": room, "roomJoin": true, "canPublish": true, "canSubscribe": true, "canPublishData": true,
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

// Gone answers a route that no longer exists with 410 and a code saying why.
func Gone(code string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusGone, map[string]string{"code": code})
	}
}

// publishStatus tells the person's audience their status, when realtime is running.
func (s *SocialService) publishStatus(userID string) {
	if s.realtime != nil {
		go s.realtime.PublishStatus(userID)
	}
}
