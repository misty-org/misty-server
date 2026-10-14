package api

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"time"

	envconfig "github.com/kannachi323/misty/server/internal/platform/config"

	db "github.com/kannachi323/misty/server/internal/platform/postgres"

	serveragent "github.com/kannachi323/misty/server/internal/agents"
)

func (s *MediaSearchService) IndexChunk() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		userID, ok := s.requireUser(w, r)
		if !ok {
			return
		}
		if strings.EqualFold(strings.TrimSpace(envconfig.Getenv("MEDIA_SEARCH_EMERGENCY_DISABLE")), "true") {
			writeJSON(w, 503, map[string]any{"code": "media_search_disabled", "message": "Media Search is temporarily disabled."})
			return
		}
		if !s.analyzer.Available() {
			writeJSON(w, 503, map[string]any{"code": "media_search_unavailable", "message": "Media Search is not configured."})
			return
		}
		var body TestingMediaIndexRequest
		if TestingDecodeAIJSONWithLimit(w, r, &body, TestingMediaMaxJSONBytes) != nil || !TestingValidMediaIndexRequest(body) {
			http.Error(w, "invalid request", 400)
			return
		}
		audio, audioOK := decodeIndexAudio(w, body)
		if !audioOK {
			return
		}
		frames, frameTimes, frameBytes, framesOK := decodeIndexFrames(w, body)
		if !framesOK {
			return
		}
		totalBytes := len(audio) + frameBytes
		if totalBytes > 4<<20 || (len(audio) == 0 && len(frames) == 0) {
			http.Error(w, "media chunk too large", http.StatusRequestEntityTooLarge)
			return
		}
		releaseProviderSlot, allowed := s.TestingAcquireProviderSlot(userID)
		if !allowed {
			w.Header().Set("Retry-After", "2")
			writeJSON(w, http.StatusTooManyRequests, map[string]any{"code": "media_search_busy", "message": "Another media chunk is currently processing. Misty will retry it shortly.", "retry_after_seconds": 2})
			return
		}
		defer releaseProviderSlot()
		if !s.claimIndexChunk(w, userID, body) {
			return
		}
		run := &mediaIndexRun{
			svc:      s,
			w:        w,
			r:        r,
			analyzer: s.analyzer.WithBilling(s.database.BillingService(), userID, "media-search:"+body.DeviceID+":"+body.AssetID+":"+fmtInt(int64(body.ChunkIndex))+":"+body.Fingerprint),
			userID:   userID,
			body:     body,
		}
		segments := []db.MediaSearchSegment{}
		if len(audio) > 0 {
			spoken, spokenOK := run.indexSpoken(audio)
			if !spokenOK {
				return
			}
			segments = append(segments, spoken...)
		}
		if len(frames) > 0 {
			visual, visualOK := run.indexVisual(frames, frameTimes)
			if !visualOK {
				return
			}
			segments = append(segments, visual...)
		}
		if err := s.database.CompleteMediaSearchChunk(userID, body.DeviceID, body.AssetID, body.ChunkIndex, body.EndMS, segments); err != nil {
			_ = s.database.FailMediaSearchChunk(userID, body.DeviceID, body.AssetID, body.ChunkIndex, "persistence_failed")
			http.Error(w, "internal error", 500)
			return
		}
		writeJSON(w, 200, map[string]any{"status": "indexed", "chunkIndex": body.ChunkIndex, "segmentCount": len(segments), "indexedThroughMs": body.EndMS})
	}
}

// acquireProviderSlot bounds provider fan-out independently of the HTTP rate
// limiter. A device runs one sequential worker, but this also protects against
// multiple devices or clients racing under the same account.
func (s *MediaSearchService) TestingAcquireProviderSlot(userID string) (func(), bool) {
	s.guardMu.Lock()
	defer s.guardMu.Unlock()
	if _, busy := s.inFlightUsers[userID]; busy || s.inFlightTotal >= TestingAiGlobalMaxConcurrent {
		return nil, false
	}
	s.inFlightUsers[userID] = struct{}{}
	s.inFlightTotal++
	return func() {
		s.guardMu.Lock()
		delete(s.inFlightUsers, userID)
		if s.inFlightTotal > 0 {
			s.inFlightTotal--
		}
		s.guardMu.Unlock()
	}, true
}

// embedMediaTexts absorbs one transient embedding failure before failing the
// customer-visible chunk. Each attempt receives its own model-specific billing reservation.
func TestingEmbedMediaTexts(ctx context.Context, analyzer *serveragent.SmartLibraryAnalyzer, texts []string) ([][]float64, serveragent.ModelUsage, error) {
	if len(texts) == 0 {
		return nil, serveragent.ModelUsage{}, nil
	}
	vectors, usage, err := analyzer.Embed(ctx, texts)
	if err == nil {
		return vectors, usage, nil
	}
	if analyzer.BillingError() != nil {
		return nil, usage, err
	}
	timer := time.NewTimer(250 * time.Millisecond)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return nil, usage, ctx.Err()
	case <-timer.C:
	}
	retried, retryUsage, retryErr := analyzer.Embed(ctx, texts)
	addUsage(&usage, retryUsage)
	if retryErr != nil {
		return nil, usage, errors.Join(err, retryErr)
	}
	return retried, usage, nil
}
