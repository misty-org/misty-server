package api

import (
	"context"
	"encoding/base64"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	envconfig "github.com/kannachi323/misty/server/internal/platform/config"

	db "github.com/kannachi323/misty/server/internal/platform/postgres"

	serveragent "github.com/kannachi323/misty/server/internal/agents"
	appbilling "github.com/kannachi323/misty/server/internal/billing"
)

func (s *MediaSearchService) IndexChunk() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		userID, ok := s.requireUser(w, r)
		if !ok {
			return
		}
		if strings.EqualFold(strings.TrimSpace(envconfig.Getenv("MEDIA_SEARCH_EMERGENCY_DISABLE")), "true") {
			writeJSON(w, 503, map[string]any{"code": "media_search_disabled", "message": "Media Search is temporarily disabled. Weekly usage was not charged."})
			return
		}
		if s.analyzer == nil || strings.TrimSpace(s.analyzer.APIKey) == "" {
			writeJSON(w, 503, map[string]any{"code": "media_search_unavailable", "message": "Media Search is not configured. Weekly usage was not charged."})
			return
		}
		var body TestingMediaIndexRequest
		if TestingDecodeAIJSONWithLimit(w, r, &body, TestingMediaMaxJSONBytes) != nil || !TestingValidMediaIndexRequest(body) {
			http.Error(w, "invalid request", 400)
			return
		}
		var audio []byte
		var err error
		if body.AudioBase64 != nil {
			audio, err = base64.StdEncoding.DecodeString(*body.AudioBase64)
			if err != nil || len(audio) > 2<<20 || !TestingValidMP3Preview(audio) {
				http.Error(w, "invalid audio preview", 400)
				return
			}
		}
		frames := make([]serveragent.SmartLibraryAsset, 0, len(body.Frames))
		frameTimes := map[string]int64{}
		totalBytes := len(audio)
		for index, frame := range body.Frames {
			raw, decodeErr := base64.StdEncoding.DecodeString(frame.Base64)
			totalBytes += len(raw)
			id := body.AssetID + "_frame_" + strconv.Itoa(index)
			if decodeErr != nil || frame.MimeType != "image/jpeg" || len(raw) > 512<<10 || !TestingValidJPEGPreview(raw) || frame.TimestampMS < body.StartMS || frame.TimestampMS >= body.EndMS {
				http.Error(w, "invalid media frame", 400)
				return
			}
			frames = append(frames, serveragent.SmartLibraryAsset{AssetID: id, AssetKind: "image", MimeType: "image/jpeg", Bytes: raw, Metadata: map[string]string{"mediaTimestampMs": fmtInt(frame.TimestampMS)}})
			frameTimes[id] = frame.TimestampMS
		}
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
		asset := db.MediaSearchAsset{DeviceID: body.DeviceID, AssetID: body.AssetID, Fingerprint: body.Fingerprint, MediaType: body.MediaType, MimeType: body.MimeType, DurationMS: body.DurationMS}
		claimed, err := s.database.ClaimMediaSearchChunk(userID, asset, body.ChunkIndex, body.StartMS, body.EndMS)
		if errors.Is(err, db.ErrMediaChunkBusy) {
			writeJSON(w, 409, map[string]any{"code": "media_chunk_processing", "message": "This media chunk is already processing."})
			return
		}
		if err != nil {
			http.Error(w, "internal error", 500)
			return
		}
		if !claimed {
			writeJSON(w, 200, map[string]any{"status": "indexed", "alreadyIndexed": true, "chunkIndex": body.ChunkIndex})
			return
		}
		estimatedUsage := appbilling.EstimateMediaIndexCharge(body.EndMS - body.StartMS)
		tier := db.TierBasic
		if license, licenseErr := s.database.GetLicenseByUserID(userID); licenseErr == nil && license != nil {
			tier = license.Tier
		}
		reservation, usageWallet, err := s.database.ReserveCredits(userID, tier, db.CreditMeterMediaSearchMinute, "media-search:"+body.DeviceID+":"+body.AssetID+":"+fmtInt(int64(body.ChunkIndex))+":"+body.Fingerprint+":"+fmtInt(time.Now().UnixNano()), estimatedUsage, time.Now())
		if err != nil {
			_ = s.database.FailMediaSearchChunk(userID, body.DeviceID, body.AssetID, body.ChunkIndex, "billing_failed")
			var insufficient db.HostedAILimitReachedError
			if errors.As(err, &insufficient) {
				response := map[string]any{"code": "hosted_ai_limit_reached", "message": "Your weekly AI agent usage is fully used."}
				if usageWallet != nil {
					response["reset_at"] = usageWallet.ResetAt
				}
				writeJSON(w, 402, response)
				return
			}
			http.Error(w, "internal error", 500)
			return
		}
		release := true
		defer func() {
			if release {
				_ = s.database.ReleaseCreditReservation(reservation.ID)
			}
		}()
		segments := []db.MediaSearchSegment{}
		var totalUsage serveragent.ModelUsage
		if len(audio) > 0 {
			transcript, usage, transcribeErr := s.analyzer.TranscribeMedia(r.Context(), audio, valueOr(body.AudioMimeType, "audio/mpeg"), body.EndMS-body.StartMS)
			addUsage(&totalUsage, usage)
			if transcribeErr != nil {
				_ = s.database.FailMediaSearchChunk(userID, body.DeviceID, body.AssetID, body.ChunkIndex, "transcription_failed")
				writeJSON(w, 502, map[string]any{"code": "transcription_failed", "message": "The agent could not transcribe this chunk. Weekly usage was not charged."})
				return
			}
			texts := make([]string, len(transcript))
			for i, item := range transcript {
				texts[i] = item.Text
			}
			vectors := [][]float64{}
			if len(texts) > 0 {
				var embedErr error
				vectors, usage, embedErr = TestingEmbedMediaTexts(r.Context(), s.analyzer, texts)
				addUsage(&totalUsage, usage)
				if embedErr != nil {
					_ = s.database.FailMediaSearchChunk(userID, body.DeviceID, body.AssetID, body.ChunkIndex, "embedding_failed")
					writeJSON(w, 502, map[string]any{"code": "embedding_failed", "message": "The agent could not index this transcript. Weekly usage was not charged."})
					return
				}
			}
			for i, item := range transcript {
				segments = append(segments, db.MediaSearchSegment{AssetID: body.AssetID, Kind: "spoken", ChunkIndex: body.ChunkIndex, StartMS: body.StartMS + item.StartMS, EndMS: minInt64(body.EndMS, body.StartMS+item.EndMS), Content: item.Text, Transcript: item.Text, Embedding: vectors[i], EmbeddingModel: serveragent.SmartLibraryEmbeddingModel, Metadata: map[string]any{"source": "audio_transcript"}})
			}
		}
		if len(frames) > 0 {
			analysis, analyzeErr := s.analyzer.Analyze(r.Context(), frames)
			addUsage(&totalUsage, analysis.Usage)
			if analyzeErr != nil {
				_ = s.database.FailMediaSearchChunk(userID, body.DeviceID, body.AssetID, body.ChunkIndex, "visual_analysis_failed")
				writeJSON(w, 502, map[string]any{"code": "visual_analysis_failed", "message": "The agent could not analyze the scenes. Weekly usage was not charged."})
				return
			}
			metadata := map[string]serveragent.SmartLibraryMetadata{}
			for _, item := range analysis.Results {
				metadata[item.AssetID] = item
			}
			// Scene retrieval is text-to-scene search. Embed the normalized scene
			// documents as one batch after vision analysis instead of uploading each
			// frame to the embedding endpoint a second time. This is cheaper and also
			// avoids a partial per-frame embedding failure discarding the whole chunk.
			visualFrames := make([]serveragent.SmartLibraryAsset, 0, len(frames))
			visualTexts := make([]string, 0, len(frames))
			for _, frame := range frames {
				item, found := metadata[frame.AssetID]
				content := strings.TrimSpace(item.SearchDocument())
				if found && content != "" {
					visualFrames = append(visualFrames, frame)
					visualTexts = append(visualTexts, content)
				}
			}
			embeddings, usage, embedErr := TestingEmbedMediaTexts(r.Context(), s.analyzer, visualTexts)
			addUsage(&totalUsage, usage)
			if embedErr != nil {
				_ = s.database.FailMediaSearchChunk(userID, body.DeviceID, body.AssetID, body.ChunkIndex, "visual_embedding_failed")
				writeJSON(w, 502, map[string]any{"code": "visual_embedding_failed", "message": "The agent could not index the scenes. Weekly usage was not charged."})
				return
			}
			byID := map[string][]float64{}
			for index, frame := range visualFrames {
				byID[frame.AssetID] = embeddings[index]
			}
			for _, frame := range visualFrames {
				item, found := metadata[frame.AssetID]
				if !found {
					continue
				}
				timestamp := frameTimes[frame.AssetID]
				visualStart, visualEnd := TestingVisualSegmentBounds(timestamp, body.EndMS)
				content := strings.TrimSpace(item.SearchDocument())
				if content == "" {
					continue
				}
				segments = append(segments, db.MediaSearchSegment{AssetID: body.AssetID, Kind: "visual", ChunkIndex: body.ChunkIndex, StartMS: visualStart, EndMS: visualEnd, Content: content, VisualDescription: item.Description, VisibleText: item.VisibleText, Embedding: byID[frame.AssetID], EmbeddingModel: serveragent.SmartLibraryEmbeddingModel, Metadata: map[string]any{"frameTimestampMs": timestamp, "primarySubject": item.PrimarySubject, "tags": item.Tags, "characters": item.Characters, "applications": item.Applications, "objects": item.Objects, "scenes": item.Scenes}})
			}
		}
		if err = s.database.CompleteMediaSearchChunk(userID, body.DeviceID, body.AssetID, body.ChunkIndex, body.EndMS, segments); err != nil {
			_ = s.database.FailMediaSearchChunk(userID, body.DeviceID, body.AssetID, body.ChunkIndex, "persistence_failed")
			http.Error(w, "internal error", 500)
			return
		}
		charge := appbilling.MediaIndexCharge(body.EndMS-body.StartMS, totalUsage)
		if _, err = s.database.SettleCreditReservation(reservation.ID, "media-search-settle:"+body.DeviceID+":"+body.AssetID+":"+fmtInt(int64(body.ChunkIndex))+":"+body.Fingerprint, db.CreditUsage{Provider: "vercel_ai_gateway", Model: "media-search-routing", InputTokens: totalUsage.InputTokens, CachedInputTokens: totalUsage.CachedInputTokens, OutputTokens: totalUsage.OutputTokens, ProviderCost: appbilling.MediaIndexProviderCost(body.EndMS-body.StartMS, totalUsage), ChargeMicrousd: charge}); err != nil {
			_ = s.database.FailMediaSearchChunk(userID, body.DeviceID, body.AssetID, body.ChunkIndex, "billing_settlement_failed")
			http.Error(w, "internal error", 500)
			return
		}
		release = false
		wallet, walletErr := s.database.GetOrCreateHostedAIWallet(userID, tier, time.Now())
		if walletErr != nil || wallet == nil {
			http.Error(w, "internal error", 500)
			return
		}
		writeJSON(w, 200, map[string]any{"status": "indexed", "chunkIndex": body.ChunkIndex, "segmentCount": len(segments), "indexedThroughMs": body.EndMS, "hostedAIUsedRatio": wallet.UsedRatio(), "hostedAIResetAt": wallet.ResetAt})
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
// customer-visible chunk. The same credit reservation covers both attempts.
func TestingEmbedMediaTexts(ctx context.Context, analyzer *serveragent.SmartLibraryAnalyzer, texts []string) ([][]float64, serveragent.ModelUsage, error) {
	if len(texts) == 0 {
		return nil, serveragent.ModelUsage{}, nil
	}
	vectors, usage, err := analyzer.Embed(ctx, texts)
	if err == nil {
		return vectors, usage, nil
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
