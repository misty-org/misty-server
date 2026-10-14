package api

import (
	"encoding/base64"
	"errors"
	"net/http"
	"strconv"
	"strings"

	db "github.com/kannachi323/misty/server/internal/platform/postgres"

	serveragent "github.com/kannachi323/misty/server/internal/agents"
)

// decodeIndexAudio decodes the optional MP3 preview. It writes a 400 and
// returns ok=false when the preview is malformed or oversized.
func decodeIndexAudio(w http.ResponseWriter, body TestingMediaIndexRequest) ([]byte, bool) {
	if body.AudioBase64 == nil {
		return nil, true
	}
	audio, err := base64.StdEncoding.DecodeString(*body.AudioBase64)
	if err != nil || len(audio) > 2<<20 || !TestingValidMP3Preview(audio) {
		http.Error(w, "invalid audio preview", 400)
		return nil, false
	}
	return audio, true
}

// decodeIndexFrames validates every JPEG frame against the chunk window. It
// returns the decoded frame assets, their timestamps by asset ID, and the total
// decoded frame bytes.
func decodeIndexFrames(w http.ResponseWriter, body TestingMediaIndexRequest) ([]serveragent.SmartLibraryAsset, map[string]int64, int, bool) {
	frames := make([]serveragent.SmartLibraryAsset, 0, len(body.Frames))
	frameTimes := map[string]int64{}
	frameBytes := 0
	for index, frame := range body.Frames {
		raw, decodeErr := base64.StdEncoding.DecodeString(frame.Base64)
		frameBytes += len(raw)
		id := body.AssetID + "_frame_" + strconv.Itoa(index)
		if decodeErr != nil || frame.MimeType != "image/jpeg" || len(raw) > 512<<10 || !TestingValidJPEGPreview(raw) || frame.TimestampMS < body.StartMS || frame.TimestampMS >= body.EndMS {
			http.Error(w, "invalid media frame", 400)
			return nil, nil, 0, false
		}
		frames = append(frames, serveragent.SmartLibraryAsset{AssetID: id, AssetKind: "image", MimeType: "image/jpeg", Bytes: raw, Metadata: map[string]string{"mediaTimestampMs": fmtInt(frame.TimestampMS)}})
		frameTimes[id] = frame.TimestampMS
	}
	return frames, frameTimes, frameBytes, true
}

// claimIndexChunk marks the chunk as processing for this request. It returns
// false when the request should end; the response has already been written in
// that case (busy, already indexed, or an internal error).
func (s *MediaSearchService) claimIndexChunk(w http.ResponseWriter, userID string, body TestingMediaIndexRequest) bool {
	asset := db.MediaSearchAsset{DeviceID: body.DeviceID, AssetID: body.AssetID, Fingerprint: body.Fingerprint, MediaType: body.MediaType, MimeType: body.MimeType, DurationMS: body.DurationMS}
	claimed, err := s.database.ClaimMediaSearchChunk(userID, asset, body.ChunkIndex, body.StartMS, body.EndMS)
	if errors.Is(err, db.ErrMediaChunkBusy) {
		writeJSON(w, 409, map[string]any{"code": "media_chunk_processing", "message": "This media chunk is already processing."})
		return false
	}
	if err != nil {
		http.Error(w, "internal error", 500)
		return false
	}
	if !claimed {
		writeJSON(w, 200, map[string]any{"status": "indexed", "alreadyIndexed": true, "chunkIndex": body.ChunkIndex})
		return false
	}
	return true
}

// mediaIndexRun carries one IndexChunk request through its processing steps.
type mediaIndexRun struct {
	svc      *MediaSearchService
	w        http.ResponseWriter
	r        *http.Request
	analyzer *serveragent.SmartLibraryAnalyzer
	userID   string
	body     TestingMediaIndexRequest
	usage    serveragent.ModelUsage
}

// indexSpoken transcribes the audio preview and embeds each transcript line.
func (run *mediaIndexRun) indexSpoken(audio []byte) ([]db.MediaSearchSegment, bool) {
	body := run.body
	analyzer := run.analyzer
	segments := []db.MediaSearchSegment{}
	transcript, usage, transcribeErr := analyzer.TranscribeMedia(run.r.Context(), audio, valueOr(body.AudioMimeType, "audio/mpeg"), body.EndMS-body.StartMS)
	addUsage(&run.usage, usage)
	if transcribeErr != nil {
		_ = run.svc.database.FailMediaSearchChunk(run.userID, body.DeviceID, body.AssetID, body.ChunkIndex, "transcription_failed")
		if billingErr := analyzer.BillingError(); billingErr != nil {
			writeBillingError(run.w, billingErr)
			return nil, false
		}
		writeJSON(run.w, 502, map[string]any{"code": "transcription_failed", "message": "The agent could not transcribe this chunk."})
		return nil, false
	}
	texts := make([]string, len(transcript))
	for i, item := range transcript {
		texts[i] = item.Text
	}
	vectors := [][]float64{}
	if len(texts) > 0 {
		var embedErr error
		vectors, usage, embedErr = TestingEmbedMediaTexts(run.r.Context(), analyzer, texts)
		addUsage(&run.usage, usage)
		if embedErr != nil {
			_ = run.svc.database.FailMediaSearchChunk(run.userID, body.DeviceID, body.AssetID, body.ChunkIndex, "embedding_failed")
			if billingErr := analyzer.BillingError(); billingErr != nil {
				writeBillingError(run.w, billingErr)
				return nil, false
			}
			writeJSON(run.w, 502, map[string]any{"code": "embedding_failed", "message": "The agent could not index this transcript."})
			return nil, false
		}
	}
	for i, item := range transcript {
		segments = append(segments, db.MediaSearchSegment{AssetID: body.AssetID, Kind: "spoken", ChunkIndex: body.ChunkIndex, StartMS: body.StartMS + item.StartMS, EndMS: minInt64(body.EndMS, body.StartMS+item.EndMS), Content: item.Text, Transcript: item.Text, Embedding: vectors[i], EmbeddingModel: configuredEmbeddingModel(run.r.Context(), analyzer, run.userID), Metadata: map[string]any{"source": "audio_transcript"}})
	}
	return segments, true
}

// indexVisual runs vision analysis on the frames, then embeds and segments the
// resulting scene documents.
func (run *mediaIndexRun) indexVisual(frames []serveragent.SmartLibraryAsset, frameTimes map[string]int64) ([]db.MediaSearchSegment, bool) {
	metadata, ok := run.analyzeFrames(frames)
	if !ok {
		return nil, false
	}
	visualFrames, byID, ok := run.embedScenes(frames, metadata)
	if !ok {
		return nil, false
	}
	return run.buildVisualSegments(visualFrames, byID, metadata, frameTimes), true
}

// analyzeFrames runs the vision model over the frames and indexes its results by asset ID.
func (run *mediaIndexRun) analyzeFrames(frames []serveragent.SmartLibraryAsset) (map[string]serveragent.SmartLibraryMetadata, bool) {
	analysis, analyzeErr := run.analyzer.Analyze(run.r.Context(), frames)
	addUsage(&run.usage, analysis.Usage)
	if analyzeErr != nil {
		_ = run.svc.database.FailMediaSearchChunk(run.userID, run.body.DeviceID, run.body.AssetID, run.body.ChunkIndex, "visual_analysis_failed")
		if billingErr := run.analyzer.BillingError(); billingErr != nil {
			writeBillingError(run.w, billingErr)
			return nil, false
		}
		writeJSON(run.w, 502, map[string]any{"code": "visual_analysis_failed", "message": "The agent could not analyze the scenes."})
		return nil, false
	}
	metadata := map[string]serveragent.SmartLibraryMetadata{}
	for _, item := range analysis.Results {
		metadata[item.AssetID] = item
	}
	return metadata, true
}

// embedScenes embeds the normalized scene documents of the analyzed frames and
// returns the frames that have a document, with their embeddings by asset ID.
func (run *mediaIndexRun) embedScenes(frames []serveragent.SmartLibraryAsset, metadata map[string]serveragent.SmartLibraryMetadata) ([]serveragent.SmartLibraryAsset, map[string][]float64, bool) {
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
	embeddings, usage, embedErr := TestingEmbedMediaTexts(run.r.Context(), run.analyzer, visualTexts)
	addUsage(&run.usage, usage)
	if embedErr != nil {
		_ = run.svc.database.FailMediaSearchChunk(run.userID, run.body.DeviceID, run.body.AssetID, run.body.ChunkIndex, "visual_embedding_failed")
		if billingErr := run.analyzer.BillingError(); billingErr != nil {
			writeBillingError(run.w, billingErr)
			return nil, nil, false
		}
		writeJSON(run.w, 502, map[string]any{"code": "visual_embedding_failed", "message": "The agent could not index the scenes."})
		return nil, nil, false
	}
	byID := map[string][]float64{}
	for index, frame := range visualFrames {
		byID[frame.AssetID] = embeddings[index]
	}
	return visualFrames, byID, true
}

// buildVisualSegments turns each embedded scene into a visual segment bounded by its frame timestamp.
func (run *mediaIndexRun) buildVisualSegments(visualFrames []serveragent.SmartLibraryAsset, byID map[string][]float64, metadata map[string]serveragent.SmartLibraryMetadata, frameTimes map[string]int64) []db.MediaSearchSegment {
	body := run.body
	segments := []db.MediaSearchSegment{}
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
		segments = append(segments, db.MediaSearchSegment{AssetID: body.AssetID, Kind: "visual", ChunkIndex: body.ChunkIndex, StartMS: visualStart, EndMS: visualEnd, Content: content, VisualDescription: item.Description, VisibleText: item.VisibleText, Embedding: byID[frame.AssetID], EmbeddingModel: configuredEmbeddingModel(run.r.Context(), run.analyzer, run.userID), Metadata: map[string]any{"frameTimestampMs": timestamp, "primarySubject": item.PrimarySubject, "tags": item.Tags, "characters": item.Characters, "applications": item.Applications, "objects": item.Objects, "scenes": item.Scenes}})
	}
	return segments
}
