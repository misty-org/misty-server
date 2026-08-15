package api

import (
	"context"
	"net/http"
	"strings"

	envconfig "github.com/kannachi323/misty/server/internal/platform/config"
	db "github.com/kannachi323/misty/server/internal/platform/postgres"
)

const SelfHostedProtocolVersion = 1

type instanceStateStore interface {
	SelfHostedInstanceState(context.Context, string) (db.InstanceState, error)
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
		config := InstanceConfigFromEnv()
		state, err := store.SelfHostedInstanceState(r.Context(), config.Name)
		if err != nil {
			writeJSON(w, http.StatusServiceUnavailable, map[string]string{"code": "instance_unavailable"})
			return
		}
		descriptor := InstanceDescriptor{
			ServerID:          state.ServerID,
			Name:              state.DisplayName,
			Deployment:        config.Deployment,
			ProtocolVersion:   SelfHostedProtocolVersion,
			MinClientProtocol: SelfHostedProtocolVersion,
			MaxClientProtocol: SelfHostedProtocolVersion,
			Capabilities:      config.Capabilities,
			BootstrapRequired: config.Deployment == "self_hosted" && state.BootstrapRequired,
			Registration:      "open",
		}
		if config.Deployment == "self_hosted" {
			descriptor.Registration = "invitation"
		}
		writeJSON(w, http.StatusOK, descriptor)
	}
}

type InstanceConfig struct {
	Name         string
	Deployment   string
	Capabilities InstanceCapabilities
}

func InstanceConfigFromEnv() InstanceConfig {
	deployment := strings.ToLower(strings.TrimSpace(envconfig.Getenv("MISTY_DEPLOYMENT_MODE")))
	if deployment != "self_hosted" {
		deployment = "hosted"
	}
	name := strings.TrimSpace(envconfig.Getenv("MISTY_INSTANCE_NAME"))
	if name == "" {
		if deployment == "self_hosted" {
			name = "Misty Self-hosted"
		} else {
			name = "Misty Hosted"
		}
	}
	storageBackend := strings.ToLower(strings.TrimSpace(envconfig.Getenv("MISTY_LIBRARY_BACKEND")))
	if storageBackend == "" {
		if deployment == "self_hosted" && strings.TrimSpace(envconfig.Getenv("MISTY_LIBRARY_FILESYSTEM_DIR")) != "" {
			storageBackend = "filesystem"
		} else {
			storageBackend = "s3"
		}
	}
	hosted := deployment == "hosted"
	return InstanceConfig{
		Name:       name,
		Deployment: deployment,
		Capabilities: InstanceCapabilities{
			Collaboration:      true,
			Library:            true,
			Notes:              true,
			Drawings:           true,
			HostedBilling:      hosted,
			HostedIntegrations: hosted,
			HostedAI:           hosted,
			StorageBackend:     storageBackend,
		},
	}
}
