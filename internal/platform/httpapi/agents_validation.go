package api

import (
	"encoding/json"
	"errors"
	"net/http"
	"regexp"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	serveragent "github.com/kannachi323/misty/server/internal/agents"
	db "github.com/kannachi323/misty/server/internal/platform/postgres"
	workflowv2 "github.com/kannachi323/misty/server/internal/workflows"
)

var (
	deviceIDPattern         = regexp.MustCompile(`^device_[0-9a-f-]{36}$`)
	deviceJobIDPattern      = regexp.MustCompile(`^devicejob_[0-9a-f-]{36}$`)
	deviceNoncePattern      = regexp.MustCompile(`^[A-Za-z0-9+/=_-]{16,200}$`)
	p2pEndpointIDPattern    = regexp.MustCompile(`^[A-Za-z0-9_-]{32,128}$`)
	pairingSessionIDPattern = regexp.MustCompile(`^pairing_[0-9a-f-]{36}$`)
)

const (
	deviceSignatureMaxSkew = 5 * time.Minute
	deviceSignedBodyLimit  = 2 << 20
)

// AgentsService now exposes trusted-device identity and exact v2 workflow
// node leases only. Shared Agent definitions and runs live in SpacesService.
type AgentsService struct {
	database         *db.Database
	avatarStore      LibraryObjectStore
	connectedDevices ConnectedDevicesConfig
	voiceAnalyzer    *serveragent.SmartLibraryAnalyzer
}

func NewAgentsService(database *db.Database) *AgentsService {
	return &AgentsService{database: database}
}

// SetAvatarStore installs the same durable object store used by member avatars.
// Agent avatar objects are immutable because an approved Space version can stay
// pinned after the owner changes the Agent's core identity.
func (s *AgentsService) SetAvatarStore(store LibraryObjectStore) {
	s.avatarStore = store
}

func (s *AgentsService) SetVoiceAnalyzer(analyzer *serveragent.SmartLibraryAnalyzer) {
	s.voiceAnalyzer = analyzer
}

func (s *AgentsService) PersonalAgents() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		userID, ok := s.requireUser(w, r)
		if !ok {
			return
		}
		items, err := s.database.ListPersonalAgents(r.Context(), userID)
		if err != nil {
			writeAgentError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"agents": items})
	}
}

func (s *AgentsService) PersonalAgent() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		userID, ok := s.requireUser(w, r)
		if !ok {
			return
		}
		agentID := strings.TrimSpace(chi.URLParam(r, "agentID"))
		item, err := s.database.PersonalAgentByID(r.Context(), userID, agentID)
		if err != nil {
			writeAgentError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, item)
	}
}

func (s *AgentsService) ClaimWorkflowNodeJob() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		userID, ok := s.requireUser(w, r)
		if !ok {
			return
		}
		deviceID := chi.URLParam(r, "deviceID")
		job, token, err := s.database.ClaimWorkflowDeviceNodeJob(userID, deviceID, time.Minute)
		if errors.Is(err, db.ErrAgentJobNotFound) {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		if err != nil {
			writeAgentError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"job": job, "leaseToken": token, "leaseExpiresAt": job.LeaseExpiresAt})
	}
}

func (s *AgentsService) WorkflowNodeLeaseAction(action string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		userID, ok := s.requireUser(w, r)
		if !ok {
			return
		}
		deviceID, jobID := chi.URLParam(r, "deviceID"), chi.URLParam(r, "jobID")
		var body struct {
			LeaseToken string          `json:"leaseToken"`
			Output     json.RawMessage `json:"output"`
			ErrorCode  string          `json:"errorCode"`
		}
		if !deviceIDPattern.MatchString(deviceID) || !deviceJobIDPattern.MatchString(jobID) || decodeAIJSON(w, r, &body) != nil || !validText(body.LeaseToken, 20, 200) {
			http.Error(w, "invalid request", http.StatusBadRequest)
			return
		}
		var job *db.WorkflowDeviceNodeJob
		var err error
		switch action {
		case "renew":
			job, err = s.database.RenewWorkflowDeviceNodeJob(userID, deviceID, jobID, body.LeaseToken)
		case "complete":
			current, lookupErr := s.database.WorkflowDeviceNodeJob(r.Context(), userID, jobID)
			if lookupErr != nil {
				writeAgentError(w, lookupErr)
				return
			}
			var schema workflowv2.JSONSchema
			if json.Unmarshal(current.OutputSchema, &schema) != nil || workflowv2.ValidateJSON(schema, body.Output) != nil {
				http.Error(w, "invalid workflow node output", http.StatusUnprocessableEntity)
				return
			}
			job, err = s.database.FinishWorkflowDeviceNodeJob(userID, deviceID, jobID, body.LeaseToken, "completed", body.Output, "")
		case "fail":
			if !validText(body.ErrorCode, 1, 120) {
				http.Error(w, "invalid request", http.StatusBadRequest)
				return
			}
			job, err = s.database.FinishWorkflowDeviceNodeJob(userID, deviceID, jobID, body.LeaseToken, "failed", nil, body.ErrorCode)
		default:
			http.Error(w, "unsupported action", http.StatusBadRequest)
			return
		}
		writeAgentResult(w, job, err, http.StatusOK)
	}
}
