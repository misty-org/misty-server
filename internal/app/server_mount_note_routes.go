package app

import (
	"net/http"

	api "github.com/misty-org/misty-server/internal/platform/httpapi"
)

func (s *Server) mountNoteRoutes(prefix string, spaces *api.SpacesService) {
	s.Router.MethodFunc(http.MethodGet, prefix+"/spaces/{spaceID}/notes", spaces.SpaceNotes())
	s.Router.MethodFunc(http.MethodPost, prefix+"/spaces/{spaceID}/notes", spaces.SpaceNotes())
	s.Router.MethodFunc(http.MethodGet, prefix+"/spaces/{spaceID}/notes/{noteID}", spaces.SpaceNote())
	s.Router.MethodFunc(http.MethodDelete, prefix+"/spaces/{spaceID}/notes/{noteID}", spaces.SpaceNote())
	s.Router.MethodFunc(http.MethodPatch, prefix+"/spaces/{spaceID}/notes/{noteID}", spaces.SpaceNote())
	s.Router.MethodFunc(http.MethodPatch, prefix+"/spaces/{spaceID}/notes/{noteID}/metadata", spaces.SpaceNoteMetadata())
	s.Router.Put(prefix+"/spaces/{spaceID}/notes/{noteID}/content", spaces.SpaceNoteContent())
	s.Router.MethodFunc(http.MethodGet, prefix+"/spaces/{spaceID}/notes/{noteID}/backlinks", spaces.SpaceNoteBacklinks())
	if s.Library == nil {
		return
	}
	s.Router.MethodFunc(http.MethodGet, prefix+"/spaces/{spaceID}/notes/{noteID}/assets", s.Library.SpaceNoteAssets())
	s.Router.MethodFunc(http.MethodPost, prefix+"/spaces/{spaceID}/notes/{noteID}/assets/uploads", s.Library.SpaceNoteAssets())
	s.Router.Post(prefix+"/spaces/{spaceID}/notes/{noteID}/assets/uploads/{uploadID}/finalize", s.Library.FinalizeUpload())
	s.Router.Get(prefix+"/spaces/{spaceID}/notes/{noteID}/assets/{assetID}/download", s.Library.SpaceNoteAssetDownload())
	s.Router.Delete(prefix+"/spaces/{spaceID}/notes/{noteID}/assets/{assetID}", s.Library.SpaceNoteAsset())
}
