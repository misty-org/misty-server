package app

import (
	"net/http"

	api "github.com/misty-org/misty-server/internal/platform/httpapi"
)

func (s *Server) mountDriveRoutes(prefix string, drive *api.SpaceDriveService) {
	base := prefix + "/spaces/{spaceID}/drive"
	item := base + "/items/{itemID}"
	s.Router.MethodFunc(http.MethodGet, base+"/changes", drive.DriveChanges())
	s.Router.MethodFunc(http.MethodGet, base+"/trash", drive.DriveTrash())
	s.Router.MethodFunc(http.MethodGet, base+"/in-use", drive.DriveInUseMarks())
	s.Router.MethodFunc(http.MethodPost, base+"/uploads", drive.StartDriveUpload())
	s.Router.MethodFunc(http.MethodPost, base+"/uploads/{uploadID}/complete", drive.CompleteDriveUpload())
	s.Router.MethodFunc(http.MethodPost, base+"/items", drive.CreateDriveItem())
	s.Router.MethodFunc(http.MethodGet, item, drive.DriveItem())
	s.Router.MethodFunc(http.MethodPatch, item, drive.PatchDriveItem())
	s.Router.MethodFunc(http.MethodDelete, item, drive.PurgeDriveItem())
	s.Router.MethodFunc(http.MethodGet, item+"/content", drive.DriveContent())
	s.Router.MethodFunc(http.MethodPost, item+"/trash", drive.TrashDriveItem())
	s.Router.MethodFunc(http.MethodPost, item+"/restore", drive.RestoreDriveItem())
	s.Router.MethodFunc(http.MethodPost, item+"/check-in-use", drive.CheckDriveInUse())
	s.Router.MethodFunc(http.MethodPut, item+"/in-use", drive.MarkDriveInUse())
	s.Router.MethodFunc(http.MethodDelete, item+"/in-use", drive.ClearDriveInUse())
	s.Router.MethodFunc(http.MethodPost, item+"/collaboration-ticket", drive.DriveFileRoomTicket())
	s.Router.MethodFunc(http.MethodPost, prefix+"/internal/journal/file-texts", drive.DriveFileRoomText())
	s.Router.MethodFunc(http.MethodPost, prefix+"/internal/journal/file-versions", drive.DriveFileRoomVersions())
}
