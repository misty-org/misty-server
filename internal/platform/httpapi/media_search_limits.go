package api

import (
	"sync"

	serveragent "github.com/kannachi323/misty/server/internal/agents"
	db "github.com/kannachi323/misty/server/internal/platform/postgres"
)

const (
	mediaMaxDurationMS = int64(120 * 60 * 1000)
	mediaChunkMS       = int64(30 * 1000)
	mediaMaxFrames     = 4
	// The decoded previews are capped at 4 MiB below. Base64 adds roughly 33%,
	// so Media Search needs a narrowly scoped limit above the generic AI JSON
	// limit without increasing it for every other AI endpoint.
	TestingMediaMaxJSONBytes = int64(6 << 20)
)

type MediaSearchService struct {
	database      *db.Database
	analyzer      *serveragent.SmartLibraryAnalyzer
	cacheMu       sync.Mutex
	queryCache    map[[32]byte]cachedSemanticQuery
	guardMu       sync.Mutex
	inFlightUsers map[string]struct{}
	inFlightTotal int
}

func NewMediaSearchService(database *db.Database, analyzer *serveragent.SmartLibraryAnalyzer) *MediaSearchService {
	return &MediaSearchService{
		database: database, analyzer: analyzer,
		queryCache:    map[[32]byte]cachedSemanticQuery{},
		inFlightUsers: map[string]struct{}{},
	}
}

type TestingMediaIndexRequest struct {
	DeviceID      string  `json:"deviceId"`
	AssetID       string  `json:"assetId"`
	Fingerprint   string  `json:"fingerprint"`
	MediaType     string  `json:"mediaType"`
	MimeType      string  `json:"mimeType"`
	DurationMS    int64   `json:"durationMs"`
	ChunkIndex    int     `json:"chunkIndex"`
	StartMS       int64   `json:"startMs"`
	EndMS         int64   `json:"endMs"`
	AudioMimeType *string `json:"audioMimeType"`
	AudioBase64   *string `json:"audioBase64"`
	Frames        []struct {
		TimestampMS int64  `json:"timestampMs"`
		MimeType    string `json:"mimeType"`
		Base64      string `json:"base64"`
	} `json:"frames"`
}
