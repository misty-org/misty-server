package app

import (
	"net/http"

	api "github.com/kannachi323/misty/server/internal/platform/httpapi"
)

// mountDrawingRoutes registers metadata and collaboration-ticket endpoints for
// Space drawings. Scene updates travel directly over DrawingRoom WebSockets.
func (s *Server) mountDrawingRoutes(prefix string, spaces *api.SpacesService) {
	s.Router.MethodFunc(
		http.MethodGet,
		prefix+"/spaces/{spaceID}/drawings",
		spaces.SpaceDrawings(),
	)
	s.Router.MethodFunc(
		http.MethodPost,
		prefix+"/spaces/{spaceID}/drawings",
		spaces.SpaceDrawings(),
	)
	s.Router.MethodFunc(
		http.MethodGet,
		prefix+"/spaces/{spaceID}/drawings/{drawingID}",
		spaces.SpaceDrawing(),
	)
	s.Router.MethodFunc(
		http.MethodPatch,
		prefix+"/spaces/{spaceID}/drawings/{drawingID}",
		spaces.SpaceDrawing(),
	)
	s.Router.MethodFunc(
		http.MethodDelete,
		prefix+"/spaces/{spaceID}/drawings/{drawingID}",
		spaces.SpaceDrawing(),
	)
	s.Router.Post(
		prefix+"/spaces/{spaceID}/drawings/{drawingID}/collaboration-ticket",
		spaces.SpaceDrawingCollaborationTicket(),
	)
	if s.Library == nil {
		return
	}
	s.Router.MethodFunc(
		http.MethodGet,
		prefix+"/spaces/{spaceID}/drawings/{drawingID}/assets",
		s.Library.SpaceDrawingAssets(),
	)
	s.Router.MethodFunc(
		http.MethodPost,
		prefix+"/spaces/{spaceID}/drawings/{drawingID}/assets/uploads",
		s.Library.SpaceDrawingAssets(),
	)
	s.Router.Post(
		prefix+"/spaces/{spaceID}/drawings/{drawingID}/assets/uploads/{uploadID}/finalize",
		s.Library.FinalizeUpload(),
	)
	s.Router.Get(
		prefix+"/spaces/{spaceID}/drawings/{drawingID}/assets/{assetID}/download",
		s.Library.SpaceDrawingAssetDownload(),
	)
	s.Router.Delete(
		prefix+"/spaces/{spaceID}/drawings/{drawingID}/assets/{assetID}",
		s.Library.SpaceDrawingAsset(),
	)
}
