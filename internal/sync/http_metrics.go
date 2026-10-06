package browsersync

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"github.com/google/uuid"
	"github.com/kannachi323/misty/server/internal/billingadapter"

	"github.com/gorilla/websocket"
	"github.com/kannachi323/misty/server/internal/platform/metrics"
)

// Encode once and count the successful payload, including Encoder's newline.
func writeSyncJSON(ctx context.Context, conn *websocket.Conn, value any) error {
	var payload bytes.Buffer
	if err := json.NewEncoder(&payload).Encode(value); err != nil {
		return err
	}
	if err := conn.WriteMessage(websocket.TextMessage, payload.Bytes()); err != nil {
		return err
	}
	frame, _ := value.(map[string]any)
	kind, _ := frame["type"].(string)
	metrics.RecordSyncMessage(ctx, "out", kind, payload.Len())
	return nil
}

func (s *BrowserSyncService) writeMeteredSyncJSON(ctx context.Context, conn *websocket.Conn, user string, value any) error {
	frame, _ := value.(map[string]any)
	kind, _ := frame["type"].(string)
	switch kind {
	case "events", "records", "records_ack", "workspaces", "blobs", "slot", "workspace_current", "workspace_delta", "workspace_snapshot":
	default:
		return writeSyncJSON(ctx, conn, value)
	}
	service := s.database.BillingService()
	if !service.Adapter.Enabled() {
		return writeSyncJSON(ctx, conn, value)
	}
	var payload bytes.Buffer
	if err := json.NewEncoder(&payload).Encode(value); err != nil {
		return err
	}
	key := "sync-transfer:" + uuid.NewString()
	reservation, err := service.Reserve(ctx, billingadapter.Request{Version: 1, AccountID: user, Operation: "sync.transfer", OperationID: key, Key: key, Usage: billingadapter.Usage{Units: map[string]int64{"bytes": int64(payload.Len())}, Estimated: true}})
	if err != nil {
		if errors.Is(err, billingadapter.ErrDenied) {
			_ = writeSyncJSON(ctx, conn, map[string]any{"type": "workspace_error", "code": "account_usage_limit_reached", "message": "Your account sync transfer allowance has been reached.", "request_id": frame["request_id"]})
		}
		return err
	}
	if err = conn.WriteMessage(websocket.TextMessage, payload.Bytes()); err != nil {
		// A failed socket write is not charged. Durable release survives cancellation.
		_ = service.Complete(ctx, "release", reservation, key+":release", billingadapter.Usage{}, "")
		return err
	}
	metrics.RecordSyncMessage(ctx, "out", kind, payload.Len())
	return service.Complete(ctx, "settle", reservation, key+":settle", billingadapter.Usage{Units: map[string]int64{"bytes": int64(payload.Len())}}, "")
}
