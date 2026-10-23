package browsersync

import (
	"github.com/kannachi323/misty/server/internal/platform/transport"
	"net/http"
)

func (s *BrowserSyncService) ControlDevice() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		user, ok := s.user(w, r)
		if !ok {
			return
		}
		var body SyncDeviceControl
		if decodeSync(http.MaxBytesReader(w, r.Body, 4096), &body) != nil {
			writeSyncError(w, ErrSyncInvalid)
			return
		}
		id, err := s.store.ControlBrowserSyncDevice(r.Context(), user, body)
		if err != nil {
			writeSyncError(w, err)
			return
		}
		transport.WriteJSON(w, 200, map[string]string{"operation_id": id})
	}
}
