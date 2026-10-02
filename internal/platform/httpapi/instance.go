package api

import (
	"context"
	"net/http"
	"strings"

	envconfig "github.com/kannachi323/misty/server/internal/platform/config"
	db "github.com/kannachi323/misty/server/internal/platform/postgres"
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
	HostedBilling      bool   `json:"hosted_billing"`
	HostedIntegrations bool   `json:"hosted_integrations"`
	HostedAI           bool   `json:"hosted_ai"`
	StorageBackend     string `json:"storage_backend"`
}

// InstanceDescriptor keeps the fields installed desktop apps already read.
// Misty is hosted only, so deployment and registration are fixed.
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

func Instance(store instanceStateStore) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		name := strings.TrimSpace(envconfig.Getenv("MISTY_INSTANCE_NAME"))
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
				HostedBilling:      strings.EqualFold(strings.TrimSpace(envconfig.Getenv("MISTY_BILLING_ADAPTER")), "http"),
				HostedIntegrations: true,
				HostedAI:           true,
				StorageBackend:     "s3",
			},
			Registration: "open",
		})
	}
}
