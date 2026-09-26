package browsersync

import (
	"time"

	"github.com/gorilla/websocket"
)

// Tell native clients this is an intentional ticket-renewal boundary. A bare
// TCP close may be delayed or rewritten by a tunnel, causing a stale-read wait.
func closeBrowserSyncForReconnect(conn *websocket.Conn) {
	_ = conn.WriteControl(
		websocket.CloseMessage,
		websocket.FormatCloseMessage(websocket.CloseServiceRestart, "Reconnect"),
		time.Now().Add(time.Second),
	)
}
