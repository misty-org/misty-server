package api

import (
	"encoding/json"
	"errors"
	"net/http"
	"regexp"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/misty-org/misty-server/internal/platform/db"
)

// An item's room and the API talk both ways. The API sends signed control commands (ACL
// changes, purges, and the history commands below); the room posts a signed projection after
// each save, and the answer carries back the limits the room keeps to.

// itemHistoryResponseLimit bounds one earlier version, base64-encoded in JSON: an 8 MiB snapshot
// is about 11 MiB.
const itemHistoryResponseLimit = 12 << 20

var itemHistoryVersionPattern = regexp.MustCompile(`^[A-Za-z0-9_-]{1,64}$`)

// writeRoomCallbackAnswer answers a room's callback with whether it applied and the limits the
// room keeps to: history_days for its history.
func (s *SpacesService) writeRoomCallbackAnswer(w http.ResponseWriter, r *http.Request, kindName, itemID string, applied bool) {
	limits, err := s.database.SpaceItemLimitsForItem(r.Context(), kindName, itemID)
	if err != nil {
		writeSpaceError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"applied": applied, "limits": limits})
}

// itemHistoryVersion is one earlier version as the room lists it.
type itemHistoryVersion struct {
	ID           string    `json:"id"`
	CreatedAt    time.Time `json:"created_at"`
	ContentBytes int64     `json:"content_bytes"`
}

// itemHistoryKept is whether a version is still inside the Space level's history window.
func itemHistoryKept(limits db.SpaceItemLimits, createdAt, now time.Time) bool {
	if limits.HistoryDays == nil {
		return true
	}
	return !createdAt.Before(now.Add(-time.Duration(*limits.HistoryDays) * 24 * time.Hour))
}

// itemHistoryRoom resolves the item a history route names and the caller's access to it.
func (s *SpacesService) itemHistoryRoom(w http.ResponseWriter, r *http.Request, kindName string, needEdit bool) (db.SpaceItemKind, string, db.SpaceItemLimits, bool) {
	userID, ok := authenticatedUser(w, r, s.database)
	if !ok {
		return db.SpaceItemKind{}, "", db.SpaceItemLimits{}, false
	}
	kind, known := db.SpaceItemKindNamed(kindName)
	if !known {
		writeSpaceError(w, db.ErrSpaceNotFound)
		return db.SpaceItemKind{}, "", db.SpaceItemLimits{}, false
	}
	itemID := chi.URLParam(r, kind.Name+"ID")
	access, limits, err := s.database.SpaceItemAccess(r.Context(), kind.Name, userID, chi.URLParam(r, "spaceID"), itemID)
	if err == nil && needEdit && !access.CanEdit {
		err = db.ErrSpaceNotFound
	}
	if err != nil {
		writeSpaceError(w, err)
		return db.SpaceItemKind{}, "", db.SpaceItemLimits{}, false
	}
	// A closing account's restore is refused before it gets here: it is a write.
	return kind, itemID, limits, true
}

func writeItemHistoryError(w http.ResponseWriter, err error) {
	var refused *collaborationControlRefusal
	switch {
	case errors.Is(err, ErrCollaborationUnconfigured):
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"code": "collaboration_unconfigured"})
	case errors.As(err, &refused) && (refused.status == http.StatusNotFound || refused.status == http.StatusGone):
		writeJSON(w, http.StatusNotFound, map[string]string{"code": "not_found"})
	case errors.As(err, &refused) && refused.status == http.StatusRequestEntityTooLarge:
		writeJSON(w, http.StatusRequestEntityTooLarge, map[string]string{"code": "document_too_large"})
	case errors.As(err, &refused):
		writeJSON(w, http.StatusBadGateway, map[string]string{"code": "collaboration_unavailable"})
	default:
		writeSpaceError(w, err)
	}
}

// itemHistoryVersions asks the room for the item's versions and keeps those inside the level's
// history window.
func (s *SpacesService) itemHistoryVersions(r *http.Request, kind db.SpaceItemKind, itemID string, limits db.SpaceItemLimits) ([]itemHistoryVersion, error) {
	result, err := s.requestCollaborationControlCommand(r.Context(), kind.Party, s.JournalCollab.ItemRoomID(kind.Name, itemID),
		itemID, "history_list", []byte(`{}`))
	if err != nil {
		return nil, err
	}
	var listed struct {
		Versions []itemHistoryVersion `json:"versions"`
	}
	if err := json.Unmarshal(result, &listed); err != nil {
		return nil, err
	}
	now := time.Now()
	versions := make([]itemHistoryVersion, 0, len(listed.Versions))
	for _, version := range listed.Versions {
		if itemHistoryVersionPattern.MatchString(version.ID) && itemHistoryKept(limits, version.CreatedAt, now) {
			versions = append(versions, version)
		}
	}
	return versions, nil
}

// SpaceItemHistory lists an item's earlier versions, newest first:
// GET /spaces/{space}/notes/{id}/history → {versions:[{id,created_at,content_bytes}]}.
// Anyone who can read the item may.
func (s *SpacesService) SpaceItemHistory(kindName string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		kind, itemID, limits, ok := s.itemHistoryRoom(w, r, kindName, false)
		if !ok {
			return
		}
		versions, err := s.itemHistoryVersions(r, kind, itemID, limits)
		if err != nil {
			writeItemHistoryError(w, err)
			return
		}
		w.Header().Set("Cache-Control", "private, no-store")
		writeJSON(w, http.StatusOK, map[string]any{"versions": versions})
	}
}

// itemHistoryVersionRequest asks the room for one version with its state, refusing one past the
// level's history window.
func (s *SpacesService) itemHistoryVersionRequest(r *http.Request, kind db.SpaceItemKind, itemID, versionID string, limits db.SpaceItemLimits) (json.RawMessage, error) {
	result, err := s.requestCollaborationControlCommandWithLimit(r.Context(), kind.Party, s.JournalCollab.ItemRoomID(kind.Name, itemID),
		itemID, "history_get", mustRawJSON(map[string]string{"version_id": versionID}), itemHistoryResponseLimit)
	if err != nil {
		return nil, err
	}
	var version struct {
		CreatedAt time.Time `json:"created_at"`
	}
	if err := json.Unmarshal(result, &version); err != nil {
		return nil, err
	}
	if !itemHistoryKept(limits, version.CreatedAt, time.Now()) {
		return nil, db.ErrSpaceNotFound
	}
	return result, nil
}

// SpaceItemHistoryVersion returns one earlier version as a full Yjs state:
// GET .../history/{versionID} → {update: base64}.
func (s *SpacesService) SpaceItemHistoryVersion(kindName string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		kind, itemID, limits, ok := s.itemHistoryRoom(w, r, kindName, false)
		if !ok {
			return
		}
		versionID := chi.URLParam(r, "versionID")
		if !itemHistoryVersionPattern.MatchString(versionID) {
			writeSpaceError(w, db.ErrSpaceNotFound)
			return
		}
		result, err := s.itemHistoryVersionRequest(r, kind, itemID, versionID, limits)
		if err != nil {
			writeItemHistoryError(w, err)
			return
		}
		var version struct {
			ID           string    `json:"id"`
			CreatedAt    time.Time `json:"created_at"`
			ContentBytes int64     `json:"content_bytes"`
			Update       string    `json:"update"`
		}
		if err := json.Unmarshal(result, &version); err != nil || strings.TrimSpace(version.Update) == "" {
			writeJSON(w, http.StatusBadGateway, map[string]string{"code": "collaboration_unavailable"})
			return
		}
		w.Header().Set("Cache-Control", "private, no-store")
		writeJSON(w, http.StatusOK, version)
	}
}

// SpaceItemHistoryRestore makes an earlier version the item's content again, as a new revision
// everyone connected sees. The room keeps what it replaced as a version, so a restore can be
// undone. It needs edit access: POST .../history/{versionID}/restore → 204.
func (s *SpacesService) SpaceItemHistoryRestore(kindName string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		kind, itemID, limits, ok := s.itemHistoryRoom(w, r, kindName, true)
		if !ok {
			return
		}
		versionID := chi.URLParam(r, "versionID")
		if !itemHistoryVersionPattern.MatchString(versionID) {
			writeSpaceError(w, db.ErrSpaceNotFound)
			return
		}
		versions, err := s.itemHistoryVersions(r, kind, itemID, limits)
		if err != nil {
			writeItemHistoryError(w, err)
			return
		}
		listed := false
		for _, version := range versions {
			listed = listed || version.ID == versionID
		}
		if !listed {
			writeSpaceError(w, db.ErrSpaceNotFound)
			return
		}
		if _, err := s.requestCollaborationControlCommand(r.Context(), kind.Party, s.JournalCollab.ItemRoomID(kind.Name, itemID),
			itemID, "history_restore", mustRawJSON(map[string]string{"version_id": versionID})); err != nil {
			writeItemHistoryError(w, err)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}
}
