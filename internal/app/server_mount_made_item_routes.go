package app

import (
	"net/http"

	"github.com/misty-org/misty-server/internal/platform/db"
	api "github.com/misty-org/misty-server/internal/platform/httpapi"
)

// mountMadeItemRoutes registers decks (Slides) and workbooks (Data), each exactly like drawings:
// their records, their rooms' tickets and their projection callbacks, and a deck's images. It
// also registers every made kind's version history, notes and drawings included. People's edits
// travel directly over each item's room WebSocket.
func (s *Server) mountMadeItemRoutes(prefix string, spaces *api.SpacesService) {
	for _, kind := range db.SpaceItemKinds() {
		item := prefix + "/spaces/{spaceID}/" + kind.Section + "/{" + kind.Name + "ID}"
		s.Router.Get(item+"/history", spaces.SpaceItemHistory(kind.Name))
		s.Router.Get(item+"/history/{versionID}", spaces.SpaceItemHistoryVersion(kind.Name))
		s.Router.Post(item+"/history/{versionID}/restore", spaces.SpaceItemHistoryRestore(kind.Name))
		if !kind.Shared {
			continue
		}
		list := prefix + "/spaces/{spaceID}/" + kind.Section
		s.Router.MethodFunc(http.MethodGet, list, spaces.SpaceMadeItems(kind.Name))
		s.Router.MethodFunc(http.MethodPost, list, spaces.SpaceMadeItems(kind.Name))
		s.Router.MethodFunc(http.MethodGet, item, spaces.SpaceMadeItem(kind.Name))
		s.Router.MethodFunc(http.MethodPatch, item, spaces.SpaceMadeItem(kind.Name))
		s.Router.MethodFunc(http.MethodDelete, item, spaces.SpaceMadeItem(kind.Name))
		s.Router.Post(item+"/collaboration-ticket", spaces.SpaceMadeItemCollaborationTicket(kind.Name))
		s.Router.Post(prefix+"/internal/journal/"+kind.Name+"-projections", spaces.JournalMadeItemProjection(kind.Name))
	}
	if s.Library == nil {
		return
	}
	// A deck's images reuse the Library's upload, verification, quota and signed-transfer
	// pipeline; authorization is still the deck's.
	deck := prefix + "/spaces/{spaceID}/decks/{deckID}"
	s.Router.MethodFunc(http.MethodGet, deck+"/assets", s.Library.SpaceDeckAssets())
	s.Router.MethodFunc(http.MethodPost, deck+"/assets/uploads", s.Library.SpaceDeckAssets())
	s.Router.Post(deck+"/assets/uploads/{uploadID}/finalize", s.Library.FinalizeUpload())
	s.Router.Get(deck+"/assets/{assetID}/download", s.Library.SpaceDeckAssetDownload())
	s.Router.Delete(deck+"/assets/{assetID}", s.Library.SpaceDeckAsset())
}
