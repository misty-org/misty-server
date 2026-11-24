package api

import (
	"net/http"

	"github.com/go-chi/chi/v5"
)

func (s *SocialService) PersonAvatar(store LibraryObjectStore) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if _, ok := authenticatedUser(w, r, s.database); !ok {
			return
		}
		avatar, err := s.database.PersonAvatarReference(r.Context(), chi.URLParam(r, "userID"))
		if err != nil {
			writeSpaceError(w, err)
			return
		}
		if avatar.Version == 0 {
			writeJSON(w, http.StatusNotFound, map[string]string{"code": "not_found"})
			return
		}
		serveAvatarObject(w, r, store, avatar.ObjectKey, avatar.Version)
	}
}

func (s *SocialService) PersonBanner(store LibraryObjectStore) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		userID, ok := authenticatedUser(w, r, s.database)
		if !ok {
			return
		}
		banner, err := s.database.PersonBannerReference(r.Context(), userID, chi.URLParam(r, "userID"))
		if err != nil {
			writeSpaceError(w, err)
			return
		}
		if banner.Version == 0 {
			writeJSON(w, http.StatusNotFound, map[string]string{"code": "not_found"})
			return
		}
		serveBannerObject(w, r, store, banner.ObjectKey, banner.Version)
	}
}

func (s *SocialService) MutualFriends() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		userID, ok := authenticatedUser(w, r, s.database)
		if !ok {
			return
		}
		people, err := s.database.MutualFriends(r.Context(), userID, chi.URLParam(r, "userID"))
		if err != nil {
			writeSpaceError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"friends": people})
	}
}

func (s *SocialService) SharedSpaces() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		userID, ok := authenticatedUser(w, r, s.database)
		if !ok {
			return
		}
		spaces, err := s.database.SharedSpaces(r.Context(), userID, chi.URLParam(r, "userID"))
		if err != nil {
			writeSpaceError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"spaces": spaces})
	}
}
