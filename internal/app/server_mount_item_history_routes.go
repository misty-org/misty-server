package app

import (
	"github.com/misty-org/misty-server/internal/platform/db"
	api "github.com/misty-org/misty-server/internal/platform/httpapi"
)

// mountItemHistoryRoutes registers every made kind's version history (a note's, today). Each
// item's earlier versions live in its collaboration room; these routes check access and ask it.
func (s *Server) mountItemHistoryRoutes(prefix string, spaces *api.SpacesService) {
	for _, kind := range db.SpaceItemKinds() {
		item := prefix + "/spaces/{spaceID}/" + kind.Section + "/{" + kind.Name + "ID}"
		s.Router.Get(item+"/history", spaces.SpaceItemHistory(kind.Name))
		s.Router.Get(item+"/history/{versionID}", spaces.SpaceItemHistoryVersion(kind.Name))
		s.Router.Post(item+"/history/{versionID}/restore", spaces.SpaceItemHistoryRestore(kind.Name))
	}
}
