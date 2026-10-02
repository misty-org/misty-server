package browsersync

import (
	"bytes"
	"context"
	"encoding/json"

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
