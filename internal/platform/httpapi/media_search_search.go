package api

import (
	"context"
	"crypto/sha256"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	db "github.com/kannachi323/misty/server/internal/platform/postgres"

	"github.com/go-chi/chi/v5"
	serveragent "github.com/kannachi323/misty/server/internal/agents"
)

func (s *MediaSearchService) Search() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		userID, ok := s.requireUser(w, r)
		if !ok {
			return
		}
		var body struct {
			DeviceID string `json:"deviceId"`
			Query    string `json:"query"`
			Limit    int    `json:"limit"`
		}
		if decodeAIJSON(w, r, &body) != nil {
			return
		}
		body.Query = strings.TrimSpace(body.Query)
		if !validMediaDeviceID(body.DeviceID) || body.Query == "" || utf8.RuneCountInString(body.Query) > 256 {
			http.Error(w, "invalid request", 400)
			return
		}
		if body.Limit == 0 {
			body.Limit = 20
		}
		vector, semanticOperation, err := s.TestingCachedEmbedding(r.Context(), userID, body.DeviceID, body.Query)
		if semanticOperation != nil {
			defer semanticOperation.Release(s.database)
		}
		if err != nil {
			vector = nil
		}
		hits, err := s.database.SearchMedia(userID, body.DeviceID, body.Query, vector, body.Limit)
		if err != nil {
			http.Error(w, "internal error", 500)
			return
		}
		if semanticOperation != nil {
			if err := semanticOperation.Settle(s.database); err != nil {
				http.Error(w, "internal error", 500)
				return
			}
		}
		writeJSON(w, 200, map[string]any{"hits": hits})
	}
}

func (s *MediaSearchService) Status() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		userID, ok := s.requireUser(w, r)
		if !ok {
			return
		}
		deviceID := strings.TrimSpace(r.URL.Query().Get("deviceId"))
		if !validMediaDeviceID(deviceID) {
			http.Error(w, "invalid request", 400)
			return
		}
		if err := s.database.PruneIncompleteMediaSearchAssets(userID, deviceID); err != nil {
			http.Error(w, "internal error", 500)
			return
		}
		assets, err := s.database.MediaSearchAssets(userID, deviceID)
		if err != nil {
			http.Error(w, "internal error", 500)
			return
		}
		writeJSON(w, 200, map[string]any{"assets": assets, "maxDurationMinutes": 120, "totalDurationLimitMinutes": nil, "incompleteRetentionDays": 30})
	}
}

func (s *MediaSearchService) DeleteAsset() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		userID, ok := s.requireUser(w, r)
		if !ok {
			return
		}
		deviceID, assetID := strings.TrimSpace(r.URL.Query().Get("deviceId")), chi.URLParam(r, "assetID")
		if !validMediaDeviceID(deviceID) || !validMediaOpaqueID(assetID) {
			http.Error(w, "invalid request", 400)
			return
		}
		deleted, err := s.database.DeleteMediaSearchAsset(userID, deviceID, assetID)
		if err != nil {
			http.Error(w, "internal error", 500)
			return
		}
		writeJSON(w, 200, map[string]any{"deleted": deleted})
	}
}

func (s *MediaSearchService) DeleteDevice() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		userID, ok := s.requireUser(w, r)
		if !ok {
			return
		}
		deviceID := chi.URLParam(r, "deviceID")
		if !validMediaDeviceID(deviceID) {
			http.Error(w, "invalid request", 400)
			return
		}
		deleted, err := s.database.DeleteMediaSearchDevice(userID, deviceID)
		if err != nil {
			http.Error(w, "internal error", 500)
			return
		}
		writeJSON(w, 200, map[string]any{"deleted": deleted})
	}
}

func (s *MediaSearchService) AdoptLegacyDevice() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		userID, ok := s.requireUser(w, r)
		if !ok {
			return
		}
		deviceID := chi.URLParam(r, "deviceID")
		if !validMediaDeviceID(deviceID) || deviceID == db.LegacyMediaSearchDeviceID {
			http.Error(w, "invalid request", http.StatusBadRequest)
			return
		}
		ready, adopted, err := s.database.AdoptLegacyMediaSearchDevice(userID, deviceID)
		if err != nil {
			http.Error(w, "internal error", http.StatusInternalServerError)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"ready": ready, "adopted": adopted})
	}
}

func (s *MediaSearchService) TestingCachedEmbedding(ctx context.Context, userID, deviceID, query string) ([]float64, *hostedSemanticQueryOperation, error) {
	if s.analyzer == nil || strings.TrimSpace(s.analyzer.APIKey) == "" {
		return nil, nil, errors.New("media semantic search is unavailable")
	}
	key := sha256.Sum256([]byte(userID + "\x00" + deviceID + "\x00" + strings.ToLower(query)))
	s.cacheMu.Lock()
	cached, found := s.queryCache[key]
	s.cacheMu.Unlock()
	if found && cached.expires.After(time.Now()) {
		return append([]float64(nil), cached.vector...), nil, nil
	}
	operation, err := beginHostedSemanticQuery(ctx, s.database, s.analyzer, userID, "media-query:"+strconv.FormatInt(time.Now().UnixNano(), 10), query)
	if err != nil {
		return nil, nil, err
	}
	vector := operation.Vector
	operation.OnSettled = func() {
		s.cacheMu.Lock()
		defer s.cacheMu.Unlock()
		if len(s.queryCache) > 500 {
			s.queryCache = map[[32]byte]cachedSemanticQuery{}
		}
		s.queryCache[key] = cachedSemanticQuery{vector: append([]float64(nil), vector...), expires: time.Now().Add(10 * time.Minute)}
	}
	return vector, operation, nil
}

func (s *MediaSearchService) requireUser(w http.ResponseWriter, r *http.Request) (string, bool) {
	userID, err := sessionUserID(r, s.database)
	if err != nil {
		http.Error(w, "internal error", 500)
		return "", false
	}
	if userID == "" {
		http.Error(w, "not authenticated", 401)
		return "", false
	}
	return userID, true
}

func TestingValidMediaIndexRequest(v TestingMediaIndexRequest) bool {
	if !validMediaDeviceID(v.DeviceID) || !validMediaOpaqueID(v.AssetID) || len(v.Fingerprint) != 64 || !isLowerHex(v.Fingerprint) || (v.MediaType != "audio" && v.MediaType != "video") || !strings.HasPrefix(v.MimeType, v.MediaType+"/") || v.DurationMS <= 0 || v.DurationMS > mediaMaxDurationMS || v.ChunkIndex < 0 || v.ChunkIndex >= mediaChunkCount(v.DurationMS) || v.StartMS != int64(v.ChunkIndex)*mediaChunkMS || len(v.Frames) > mediaMaxFrames {
		return false
	}
	expectedEnd := minInt64(v.DurationMS, v.StartMS+mediaChunkMS)
	if v.DurationMS-expectedEnd > 0 && v.DurationMS-expectedEnd < 5_000 {
		expectedEnd = v.DurationMS
	}
	if v.EndMS != expectedEnd {
		return false
	}
	if v.AudioBase64 != nil && (v.AudioMimeType == nil || *v.AudioMimeType != "audio/mpeg") {
		return false
	}
	if v.AudioBase64 == nil && v.AudioMimeType != nil {
		return false
	}
	if v.MediaType == "audio" && len(v.Frames) > 0 {
		return false
	}
	return true
}

func validMediaDeviceID(value string) bool {
	return len(value) == 39 && strings.HasPrefix(value, "device_") && isLowerHex(value[7:])
}

func validMediaOpaqueID(value string) bool {
	return len(value) == 38 && strings.HasPrefix(value, "media_") && isLowerHex(value[6:])
}

func mediaChunkCount(duration int64) int {
	full := duration / mediaChunkMS
	remainder := duration % mediaChunkMS
	if remainder == 0 {
		return int(full)
	}
	if remainder < 5_000 && full > 0 {
		return int(full)
	}
	return int(full + 1)
}

func isLowerHex(v string) bool {
	for _, c := range v {
		if !(c >= '0' && c <= '9' || c >= 'a' && c <= 'f') {
			return false
		}
	}
	return true
}

func valueOr(v *string, fallback string) string {
	if v != nil && *v != "" {
		return *v
	}
	return fallback
}

func fmtInt(v int64) string { return strconv.FormatInt(v, 10) }

func addUsage(total *serveragent.ModelUsage, next serveragent.ModelUsage) {
	total.InputTokens += next.InputTokens
	total.CachedInputTokens += next.CachedInputTokens
	total.OutputTokens += next.OutputTokens
	total.ReasoningTokens += next.ReasoningTokens
}
