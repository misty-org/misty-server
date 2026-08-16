package api

import (
	"crypto/subtle"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"

	db "github.com/kannachi323/misty/server/internal/platform/postgres"
)

func (s *SpacesService) FigmaWebhook() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		raw, err := io.ReadAll(io.LimitReader(r.Body, 1<<20+1))
		if err != nil || len(raw) > 1<<20 {
			writeJSON(w, http.StatusBadRequest, map[string]string{"code": "invalid_figma_webhook"})
			return
		}
		var payload map[string]any
		if json.Unmarshal(raw, &payload) != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"code": "invalid_figma_webhook"})
			return
		}
		webhookID := figmaAnyID(payload["webhook_id"])
		eventType := strings.ToUpper(firstProviderString(payload, "event_type"))
		passcode := firstProviderString(payload, "passcode")
		if webhookID == "" || eventType == "" || passcode == "" {
			writeJSON(w, http.StatusBadRequest, map[string]string{"code": "invalid_figma_webhook"})
			return
		}
		binding, subscriptionID, expectedHash, expectedEventType, err := s.database.FigmaBindingByWebhookID(r.Context(), webhookID)
		if err != nil || subtle.ConstantTimeCompare([]byte(expectedHash), []byte(hashProviderValue(passcode))) != 1 {
			writeJSON(w, http.StatusBadRequest, map[string]string{"code": "invalid_figma_passcode"})
			return
		}
		deleteEvent := eventType == "FILE_DELETE" && expectedEventType == "FILE_UPDATE"
		if eventType != "PING" && eventType != expectedEventType && !deleteEvent {
			writeJSON(w, http.StatusBadRequest, map[string]string{"code": "figma_webhook_event_mismatch"})
			return
		}
		fileKey := firstProviderString(payload, "file_key")
		if eventType != "PING" {
			allowed, membershipErr := s.database.FigmaBindingContainsFile(r.Context(), binding, fileKey)
			if membershipErr != nil || !allowed {
				writeJSON(w, http.StatusBadRequest, map[string]string{"code": "figma_webhook_resource_mismatch"})
				return
			}
		}
		deliveryHash := providerPayloadFingerprint(raw)
		occurred := parseFigmaTime(firstProviderString(payload, "timestamp"))
		fresh, err := s.database.BeginFigmaWebhookDelivery(r.Context(), deliveryHash, subscriptionID, webhookID, eventType, fileKey, occurred)
		if err != nil {
			writeSpaceError(w, err)
			return
		}
		if !fresh {
			writeJSON(w, http.StatusOK, map[string]any{"accepted": true, "duplicate": true})
			return
		}
		if eventType == "PING" {
			_ = s.database.FinishFigmaWebhookDelivery(r.Context(), deliveryHash, "processed", "")
			writeJSON(w, http.StatusOK, map[string]any{"accepted": true})
			return
		}

		title := firstNonempty(firstProviderString(payload, "file_name"), eventType)
		actorID := figmaNestedString(payload, "triggered_by", "id")
		actorName := firstNonempty(figmaNestedString(payload, "triggered_by", "handle"), figmaNestedString(payload, "triggered_by", "name"))
		if deleteEvent {
			_ = s.database.MarkFigmaFileDeleted(r.Context(), binding, fileKey)
		}
		record := db.FigmaContentRecord{
			BindingID: binding.ID, FileKey: fileKey, RecordType: "webhook_event", ExternalID: deliveryHash,
			ParentExternalID: fileKey, Title: title, ActorID: actorID, ActorName: actorName, Fingerprint: deliveryHash,
			Provenance: mustJSONRaw(map[string]any{"source": "webhook", "provider": "figma", "webhook_id": webhookID, "event_type": eventType, "file_key": fileKey}),
			OccurredAt: occurred,
		}
		if err := s.database.UpsertFigmaContentRecord(r.Context(), record); err != nil {
			_ = s.database.FinishFigmaWebhookDelivery(r.Context(), deliveryHash, "failed", "record_upsert_failed")
			writeSpaceError(w, err)
			return
		}
		// The event is a durable invalidation marker, not a content sync. Leave
		// the prior sync cursor intact until an authenticated manager refreshes.
		if !deleteEvent {
			_ = s.database.MarkFigmaBindingUpdateAvailable(r.Context(), binding.ID)
		}
		_ = s.database.FinishFigmaWebhookDelivery(r.Context(), deliveryHash, "processed", "")
		writeJSON(w, http.StatusOK, map[string]any{"accepted": true, "refresh_required": true})
	}
}

func figmaAnyID(value any) string {
	switch typed := value.(type) {
	case string:
		return strings.TrimSpace(typed)
	case float64:
		return fmt.Sprintf("%.0f", typed)
	case json.Number:
		return typed.String()
	}
	return ""
}

func figmaNestedString(value map[string]any, keys ...string) string {
	var current any = value
	for _, key := range keys {
		object, ok := current.(map[string]any)
		if !ok {
			return ""
		}
		current = object[key]
	}
	result, _ := current.(string)
	return result
}
