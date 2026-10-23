package app

import (
	"net/http"

	api "github.com/kannachi323/misty/server/internal/platform/httpapi"
)

// mountNoteRoutes registers the server-backed note API. Current Space
// membership grants read/write access; creator/owner checks protect deletion.
func (s *Server) mountNoteRoutes(prefix string, spaces *api.SpacesService) {
	s.Router.MethodFunc(http.MethodGet, prefix+"/spaces/{spaceID}/notes", spaces.SpaceNotes())
	s.Router.MethodFunc(http.MethodPost, prefix+"/spaces/{spaceID}/notes", spaces.SpaceNotes())
	s.Router.MethodFunc(http.MethodGet, prefix+"/spaces/{spaceID}/notes/{noteID}", spaces.SpaceNote())
	s.Router.MethodFunc(http.MethodDelete, prefix+"/spaces/{spaceID}/notes/{noteID}", spaces.SpaceNote())
	// Archive/unarchive. The handler and SetSpaceNoteArchived already existed;
	// without this the path was unreachable over HTTP.
	s.Router.MethodFunc(http.MethodPatch, prefix+"/spaces/{spaceID}/notes/{noteID}", spaces.SpaceNote())
	s.Router.MethodFunc(http.MethodPatch, prefix+"/spaces/{spaceID}/notes/{noteID}/metadata", spaces.SpaceNoteMetadata())
	s.Router.MethodFunc(http.MethodGet, prefix+"/spaces/{spaceID}/notes/{noteID}/backlinks", spaces.SpaceNoteBacklinks())
	s.Router.MethodFunc(http.MethodPost, prefix+"/internal/journal/note-projections", spaces.JournalNoteProjection())
	s.Router.Post(prefix+"/spaces/{spaceID}/notes/{noteID}/collaboration-ticket", spaces.SpaceNoteCollaborationTicket())
	if s.Library == nil {
		return
	}
	// Assets live on the Library service because they reuse its upload,
	// verification, quota, and signed-transfer pipeline. Authorization is still
	// the note's, not the Space's.
	s.Router.MethodFunc(http.MethodGet, prefix+"/spaces/{spaceID}/notes/{noteID}/assets", s.Library.SpaceNoteAssets())
	s.Router.MethodFunc(http.MethodPost, prefix+"/spaces/{spaceID}/notes/{noteID}/assets/uploads", s.Library.SpaceNoteAssets())
	s.Router.Post(prefix+"/spaces/{spaceID}/notes/{noteID}/assets/uploads/{uploadID}/finalize", s.Library.FinalizeUpload())
	s.Router.Get(prefix+"/spaces/{spaceID}/notes/{noteID}/assets/{assetID}/download", s.Library.SpaceNoteAssetDownload())
	s.Router.Delete(prefix+"/spaces/{spaceID}/notes/{noteID}/assets/{assetID}", s.Library.SpaceNoteAsset())
}
