package api

import (
	"context"
	"net/http"
	"strings"

	"github.com/kannachi323/misty/server/internal/platform/config"
	"github.com/kannachi323/misty/server/internal/platform/db"
)

// InstanceProtocolVersion is the client protocol the server speaks.
const InstanceProtocolVersion = 1

type instanceStateStore interface {
	InstanceState(context.Context, string) (db.InstanceState, error)
}

type InstanceCapabilities struct {
	Collaboration      bool   `json:"collaboration"`
	Library            bool   `json:"library"`
	Notes              bool   `json:"notes"`
	Drawings           bool   `json:"drawings"`
	HostedIntegrations bool   `json:"hosted_integrations"`
	StorageBackend     string `json:"storage_backend"`
}

// InstanceDescriptor keeps the fields installed desktop apps already read.
// Registration is "open" until the server's first account exists and
// "invitation" after it: later accounts come by invitation or from the
// operator account API.
type InstanceDescriptor struct {
	ServerID          string               `json:"server_id"`
	Name              string               `json:"name"`
	Deployment        string               `json:"deployment"`
	ProtocolVersion   int                  `json:"protocol_version"`
	MinClientProtocol int                  `json:"min_client_protocol"`
	MaxClientProtocol int                  `json:"max_client_protocol"`
	Capabilities      InstanceCapabilities `json:"capabilities"`
	BootstrapRequired bool                 `json:"bootstrap_required"`
	Registration      string               `json:"registration"`
}

// Instance describes this server. storage is the Library backend in use:
// "s3", "filesystem" or "memory".
func Instance(store instanceStateStore, storage string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		name := strings.TrimSpace(config.Getenv("MISTY_INSTANCE_NAME"))
		if name == "" {
			name = "Misty"
		}
		state, err := store.InstanceState(r.Context(), name)
		if err != nil {
			writeJSON(w, http.StatusServiceUnavailable, map[string]string{"code": "instance_unavailable"})
			return
		}
		writeJSON(w, http.StatusOK, InstanceDescriptor{
			ServerID:          state.ServerID,
			Name:              state.DisplayName,
			Deployment:        "hosted",
			ProtocolVersion:   InstanceProtocolVersion,
			MinClientProtocol: InstanceProtocolVersion,
			MaxClientProtocol: InstanceProtocolVersion,
			Capabilities: InstanceCapabilities{
				Collaboration:      true,
				Library:            true,
				Notes:              true,
				Drawings:           true,
				HostedIntegrations: true,
				StorageBackend:     storage,
			},
			Registration: instanceRegistration(r.Context(), store),
		})
	}
}

func instanceRegistration(ctx context.Context, store instanceStateStore) string {
	if counter, ok := store.(accountCounter); ok {
		return registrationState(ctx, counter)
	}
	return "open"
}
