package app

import (
	"fmt"
	"os/exec"

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
	Spaces        *api.SpacesService
	Realtime      *api.RealtimeService
	WebsiteURL    string
	HealthMonitor *healthMonitor
	Metrics       *metrics.Registry
	Suspensions   *api.SuspensionGuard
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
		Router:     chi.NewRouter(),
		Database:   &db.Database{},
		WebsiteURL: websiteURL,
	}
	s.LibraryStore, err = LibraryStoreFromEnv()
	if err != nil {
		return nil, err
	}
	s.Library, err = api.NewSpaceLibraryService(s.Database, s.LibraryStore, true, api.UploadLimitsFromEnv())
	if err != nil {
		return nil, fmt.Errorf("configure Space Library: %w", err)
	}
	mediaProcessingEnabled := false
	if mediaProcessorBin, lookupErr := exec.LookPath("ffmpeg"); lookupErr == nil {
		processor, processorErr := api.NewFFmpegLibraryMediaProcessor(mediaProcessorBin)
		if processorErr != nil {
			return nil, fmt.Errorf("configure Library media processor: %w", processorErr)
		}
		s.Library.SetMediaProcessor(processor)
		extractor, extractorErr := api.NewFFprobeLibraryMetadataExtractor(mediaProcessorBin)
		if extractorErr != nil {
			return nil, fmt.Errorf("configure Library metadata extractor: %w", extractorErr)
		}
		s.Library.SetMetadataExtractor(extractor)
		mediaProcessingEnabled = true
	}
	s.Library.SetSubsystems(true, mediaProcessingEnabled, mediaProcessingEnabled, true)
	s.Library.SetNoteAssetsEnabled(true)
	s.Library.SetDrawingAssetsEnabled(true)
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
	s.Spaces.SetLibraryProvider(s.Library)
	s.Spaces.SetAvatarStore(s.LibraryStore)
	s.Realtime = api.NewRealtimeService(s.Database, s.Database.GetDSN())

	s.HealthMonitor = NewHealthMonitor(s)

	return s, nil
}
