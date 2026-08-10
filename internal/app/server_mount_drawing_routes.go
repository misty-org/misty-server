package app

import (
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	envconfig "github.com/kannachi323/misty/server/internal/platform/config"

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

func serverFeatureEnabled(name string) bool {
	return strings.EqualFold(strings.TrimSpace(envconfig.Getenv(name)), "true")
}

type r2Config struct {
	endpoint, bucket, accessKey, secretKey string
}

func r2ConfigFromEnv() r2Config {
	return r2Config{
		endpoint:  strings.TrimSpace(envconfig.Getenv("R2_ENDPOINT")),
		bucket:    strings.TrimSpace(envconfig.Getenv("R2_BUCKET")),
		accessKey: strings.TrimSpace(envconfig.Getenv("R2_ACCESS_KEY")),
		secretKey: strings.TrimSpace(envconfig.Getenv("R2_SECRET_KEY")),
	}
}

func (config r2Config) empty() bool {
	return config.endpoint == "" && config.bucket == "" && config.accessKey == "" && config.secretKey == ""
}

func TestingLibraryStoreFromEnv() (api.LibraryObjectStore, error) {
	config := r2ConfigFromEnv()
	environment := strings.TrimSpace(envconfig.Getenv("MISTY_ENVIRONMENT"))
	if localRoot := strings.TrimSpace(envconfig.Getenv("MISTY_LIBRARY_LOCAL_DIR")); localRoot != "" {
		if strings.EqualFold(environment, "production") {
			return nil, fmt.Errorf("MISTY_LIBRARY_LOCAL_DIR cannot be used in production")
		}
		store, err := api.NewLocalLibraryObjectStore(localRoot)
		if err != nil {
			return nil, fmt.Errorf("configure local Library store: %w", err)
		}
		return store, nil
	}
	if config.empty() && !strings.EqualFold(environment, "production") {
		return api.NewMemoryLibraryObjectStore(), nil
	}
	if config.endpoint == "" {
		return nil, fmt.Errorf("R2_ENDPOINT is required for the Space Library")
	}
	store, err := api.NewS3LibraryObjectStore(api.S3LibraryObjectStoreConfig{
		Endpoint: config.endpoint, Region: "auto", Bucket: config.bucket,
		AccessKeyID: config.accessKey, SecretAccessKey: config.secretKey,
		ForcePathStyle: true, BucketPrivate: true, PermanentObjects: true,
	})
	if err != nil {
		return nil, fmt.Errorf("configure R2 Library store: %w", err)
	}
	return store, nil
}

func (s *Server) mountMediaSearchRoutes(prefix string, service *api.MediaSearchService) {
	s.Router.Post(prefix+"/chunks", service.IndexChunk())
	s.Router.Post(prefix+"/search", service.Search())
	s.Router.Get(prefix+"/status", service.Status())
	s.Router.Delete(prefix+"/assets/{assetID}", service.DeleteAsset())
	s.Router.Delete(prefix+"/devices/{deviceID}", service.DeleteDevice())
	s.Router.Post(prefix+"/devices/{deviceID}/adopt-legacy", service.AdoptLegacyDevice())
}

func (s *Server) mountSmartLibraryRoutes(prefix string, service *api.SmartLibraryService) {
	s.Router.Post(prefix+"/search", service.GlobalSearch())
	s.Router.Get(prefix+"/index-status", service.IndexStatus())
	s.Router.Post(prefix+"/reindex", service.PlanReindex())
	s.Router.Post(prefix+"/reindex/{jobID}/complete", service.CompleteReindex())
	s.Router.Post(prefix+"/folders", service.RegisterFolder())
	s.Router.Post(prefix+"/folders/{folderID}/preflight", service.Preflight())
	s.Router.Post(prefix+"/folders/{folderID}/sample", service.CreateSample())
	s.Router.Post(prefix+"/folders/{folderID}/sample/approve", service.Approve("sample"))
	s.Router.Post(prefix+"/folders/{folderID}/approve", service.Approve("full"))
	s.Router.Get(prefix+"/folders/{folderID}/progress", service.Progress())
	s.Router.Get(prefix+"/folders/{folderID}/results", service.Results())
	s.Router.Put(prefix+"/folders/{folderID}/assets/{assetID}/tags", service.SetAssetTags())
	s.Router.Post(prefix+"/folders/{folderID}/rescan", service.Rescan())
	s.Router.Post(prefix+"/folders/{folderID}/search", service.Search())
	s.Router.Delete(prefix+"/folders/{folderID}", service.Delete())
}

func TestingAllowedCORSOrigins() []string {
	origins := []string{
		"tauri://localhost",
		"http://tauri.localhost",
		"https://tauri.localhost",
		"http://localhost:5173",
		"http://127.0.0.1:5173",
	}
	for _, origin := range strings.Split(envconfig.Getenv("MISTY_ALLOWED_ORIGINS"), ",") {
		origin = strings.TrimSpace(origin)
		if origin != "" && !strings.Contains(origin, "*") {
			origins = append(origins, origin)
		}
	}
	return origins
}

// allowedCORSRequestHeaders is every header the desktop client may send.
//
// A header missing here fails at the preflight with a 200 that carries no
// Access-Control-Allow-Origin, which surfaces in the browser as an opaque
// "load failed" rather than anything pointing at CORS. Omitting
// X-Misty-Library-Reauthentication is exactly how Recently Deleted and Hidden
// became unreachable: only those collections send it.
var allowedCORSRequestHeaders = []string{
	"Accept",
	"Authorization",
	"Content-Type",
	"Idempotency-Key",
	"X-Request-ID",
	"X-Misty-Platform",
	"X-Misty-Release-Channel",
	"X-Misty-Session-Id",
	"X-Misty-Analytics-Enabled",
	"X-Misty-Device-Timestamp",
	"X-Misty-Device-Nonce",
	"X-Misty-Device-Signature",
	"X-Misty-Attachment-Upload-Token",
	"X-Misty-Library-Upload-Token",
	"X-Misty-Library-Reauthentication",
}

func TestingIsAllowedCORSOrigin(origin string) bool {
	for _, allowed := range TestingAllowedCORSOrigins() {
		if strings.EqualFold(origin, allowed) {
			return true
		}
	}
	parsed, err := url.Parse(origin)
	if err != nil || parsed.Scheme != "http" || !TestingIsLocalhostHostname(parsed.Hostname()) || parsed.Path != "" || parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" {
		return false
	}
	port, err := strconv.Atoi(parsed.Port())
	return err == nil && port >= 5173 && port <= 5222
}

func (s *Server) mountAIRoutes(prefix string, aiService *api.AIService) {
	s.Router.Get(prefix+"/status", aiService.Status())
	s.Router.Post(prefix+"/complete", aiService.Complete())
}
