package app

import (
	"fmt"

	"github.com/misty-org/misty-server/internal/platform/db"
	api "github.com/misty-org/misty-server/internal/platform/httpapi"

	"github.com/go-chi/chi/v5"
	"github.com/misty-org/misty-server/internal/platform/metrics"
)

type Server struct {
	AbuseGuard    *api.AbuseGuard
	Router        *chi.Mux
	Database      *db.Database
	LibraryStore  api.LibraryObjectStore
	Library       *api.SpaceLibraryService
	Drive         *api.SpaceDriveService
	Spaces        *api.SpacesService
	Realtime      *api.RealtimeService
	WebsiteURL    string
	HealthMonitor *healthMonitor
	Metrics       *metrics.Registry
	Suspensions   *api.SuspensionGuard
	LiveKitRooms  *api.LiveKitRoomWatcher
}

func CreateServer() (*Server, error) {
	if err := ValidateProductionEnvironment(); err != nil {
		return nil, fmt.Errorf("validate production environment: %w", err)
	}
	warnOnProxyConfiguration()
	websiteURL, err := WebsiteURLFromEnv()
	if err != nil {
		return nil, err
	}

	s := &Server{
		Router:       chi.NewRouter(),
		Database:     &db.Database{},
		WebsiteURL:   websiteURL,
		LiveKitRooms: api.NewLiveKitRoomWatcher(),
	}
	s.LibraryStore, err = LibraryStoreFromEnv()
	if err != nil {
		return nil, err
	}
	s.Drive, err = api.NewSpaceDriveService(s.Database, s.LibraryStore)
	if err != nil {
		return nil, fmt.Errorf("configure Space drive: %w", err)
	}
	s.Library, err = api.NewSpaceLibraryService(s.Database, s.LibraryStore, api.UploadLimitsFromEnv())
	if err != nil {
		return nil, fmt.Errorf("configure Space uploads: %w", err)
	}
	s.Library.SetAttachmentsEnabled(true)
	s.Library.SetNoteAssetsEnabled(true)
	spaceKey, err := spaceLinkEncryptionKeyFromEnv()
	if err != nil {
		return nil, err
	}
	s.Spaces, err = api.NewSpacesService(s.Database, spaceKey)
	if err != nil {
		return nil, fmt.Errorf("configure Space link encryption: %w", err)
	}
	journalCollab, err := api.JournalCollabConfigFromEnv()
	if err != nil {
		return nil, fmt.Errorf("configure journal collaboration: %w", err)
	}
	s.Spaces.SetJournalCollab(journalCollab)
	s.Drive.SetJournalCollab(journalCollab)
	s.Spaces.SetLibraryProvider(s.Library)
	s.Spaces.SetAvatarStore(s.LibraryStore)
	s.Realtime = api.NewRealtimeService(s.Database, s.Database.GetDSN())
	api.SetCallRemoval(s.LiveKitRooms, s.Realtime, s.Database)

	s.HealthMonitor = NewHealthMonitor(s)

	return s, nil
}
