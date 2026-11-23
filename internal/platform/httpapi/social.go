package api

import (
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/misty-org/misty-server/internal/platform/config"
	"github.com/misty-org/misty-server/internal/platform/db"
)

type SocialService struct {
	database                              *db.Database
	realtime                              *RealtimeService
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
		w.WriteHeader(http.StatusAccepted)
	}
}

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

func Gone(code string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusGone, map[string]string{"code": code})
	}
}

func (s *SocialService) publishStatus(userID string) {
	if s.realtime != nil {
		go s.realtime.PublishStatus(userID)
	}
}
