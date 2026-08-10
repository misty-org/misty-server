package api

import (
	"errors"
	"net/http"
	"sync"
	"time"

	db "github.com/kannachi323/misty/server/internal/platform/postgres"

	"github.com/go-chi/chi/v5"
	serveragent "github.com/kannachi323/misty/server/internal/agents"
)

var errSemanticSearchRateLimited = errors.New("semantic search rate limited")

const (
	smartLibrarySampleSize = 25
	smartLibraryLimit      = 500
	smartLibraryBatchSize  = 8
)

type SmartLibraryService struct {
	database     *db.Database
	analyzer     *serveragent.SmartLibraryAnalyzer
	searchMu     sync.Mutex
	queryCache   map[[32]byte]cachedSemanticQuery
	queryWindows map[[32]byte]semanticQueryWindow
}

type cachedSemanticQuery struct {
	vector  []float64
	expires time.Time
}

type semanticQueryWindow struct {
	started time.Time
	count   int
}

func NewSmartLibraryService(database *db.Database, analyzers ...*serveragent.SmartLibraryAnalyzer) *SmartLibraryService {
	service := &SmartLibraryService{database: database, queryCache: map[[32]byte]cachedSemanticQuery{}, queryWindows: map[[32]byte]semanticQueryWindow{}}
	if len(analyzers) > 0 {
		service.analyzer = analyzers[0]
	}
	return service
}

func (s *SmartLibraryService) RegisterFolder() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		userID, ok := s.requireUser(w, r)
		if !ok {
			return
		}
		var body struct {
			ClientLibraryID string `json:"clientLibraryId"`
			SourceKind      string `json:"sourceKind"`
			PilotLimit      int    `json:"pilotLimit"`
		}
		if decodeAIJSON(w, r, &body) != nil || !TestingValidOpaqueID(body.ClientLibraryID, "lib_") || (body.SourceKind != "local" && body.SourceKind != "cloud") || body.PilotLimit != smartLibraryLimit {
			http.Error(w, "invalid request", http.StatusBadRequest)
			return
		}
		folder, err := s.database.RegisterSmartLibraryFolder(userID, body.ClientLibraryID, body.SourceKind)
		switch {
		case errors.Is(err, db.ErrSmartLibraryActiveFolder):
			writeJSON(w, http.StatusConflict, map[string]any{"code": "smart_library_root_exists", "message": "Remove the active Smart Library before choosing another folder."})
		case err != nil:
			http.Error(w, "internal error", 500)
		default:
			writeJSON(w, http.StatusCreated, map[string]any{"folderId": folder.ID, "allowance": allowance(folder)})
		}
	}
}

func (s *SmartLibraryService) Preflight() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		userID, ok := s.requireUser(w, r)
		if !ok {
			return
		}
		folderID := chi.URLParam(r, "folderID")
		var body struct {
			TotalImages           int `json:"totalImages"`
			SupportedImages       int `json:"supportedImages"`
			UnsupportedImages     int `json:"unsupportedImages"`
			AlreadyAnalyzedImages int `json:"alreadyAnalyzedImages"`
			ChangedImages         int `json:"changedImages"`
			EligibleImages        int `json:"eligibleImages"`
			RequestedImages       int `json:"requestedImages"`
		}
		if decodeAIJSON(w, r, &body) != nil || body.TotalImages < 0 || body.SupportedImages < 0 || body.UnsupportedImages < 0 || body.EligibleImages < 0 {
			http.Error(w, "invalid request", 400)
			return
		}
		requested := min(body.RequestedImages, body.EligibleImages, smartLibraryLimit)
		folder, err := s.database.SetSmartLibraryEstimate(userID, folderID, requested)
		if !s.writeFolderError(w, err) {
			return
		}
		writeJSON(w, 200, map[string]any{"estimate": s.estimate(userID, folder)})
	}
}

func (s *SmartLibraryService) CreateSample() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		userID, ok := s.requireUser(w, r)
		if !ok {
			return
		}
		folderID := chi.URLParam(r, "folderID")
		var body struct {
			Candidates          []db.SmartLibraryCandidate `json:"candidates"`
			MaximumSampleImages int                        `json:"maximumSampleImages"`
		}
		if decodeAIJSON(w, r, &body) != nil || body.MaximumSampleImages != smartLibrarySampleSize || len(body.Candidates) > smartLibrarySampleSize {
			http.Error(w, "invalid request", 400)
			return
		}
		for _, candidate := range body.Candidates {
			if !TestingValidOpaqueID(candidate.AssetID, "asset_") || len(candidate.Fingerprint) != 64 || candidate.SizeBytes < 0 || !validAssetDescriptor(candidate.AssetKind, candidate.MimeType) {
				http.Error(w, "invalid request", 400)
				return
			}
		}
		ids, err := s.database.CreateSmartLibrarySample(userID, folderID, body.Candidates)
		if !s.writeFolderError(w, err) {
			return
		}
		folder, _ := s.database.SmartLibraryFolder(userID, folderID)
		writeJSON(w, 200, map[string]any{"assetIds": ids, "estimate": s.estimate(userID, folder)})
	}
}
