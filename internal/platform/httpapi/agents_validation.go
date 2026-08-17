package api

import (
	"encoding/json"
	"errors"
	"net/http"
	"regexp"
	"sort"
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

func (s *AgentsService) PersonalAgents() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		userID, ok := s.requireUser(w, r)
		if !ok {
			return
		}
		switch r.Method {
		case http.MethodGet:
			items, err := s.database.ListPersonalAgents(r.Context(), userID)
			if err != nil {
				writeAgentError(w, err)
				return
			}
			writeJSON(w, http.StatusOK, map[string]any{"agents": items})
		case http.MethodPost:
			var body db.PersonalAgent
			if decodeAIJSON(w, r, &body) != nil {
				return
			}
			if !personalAgentToolGrantsKnown(body.ToolPermissions) {
				writeAgentError(w, db.ErrSpaceInvalid)
				return
			}
			if strings.TrimSpace(body.ModelID) == "" {
				writeJSON(w, http.StatusUnprocessableEntity, map[string]string{"code": "agent_model_required"})
				return
			}
			body.ModelMode = "pinned"
			if !serveragent.GatewayModelAvailable(r.Context(), body.ModelID) {
				writeJSON(w, http.StatusUnprocessableEntity, map[string]string{"code": "agent_model_unavailable"})
				return
			}
			item, err := s.database.CreatePersonalAgent(r.Context(), userID, body)
			if err != nil {
				writeAgentError(w, err)
				return
			}
			writeJSON(w, http.StatusCreated, item)
		default:
			w.WriteHeader(http.StatusMethodNotAllowed)
		}
	}
}

func (s *AgentsService) PersonalAgent() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		userID, ok := s.requireUser(w, r)
		if !ok {
			return
		}
		agentID := strings.TrimSpace(chi.URLParam(r, "agentID"))
		switch r.Method {
		case http.MethodGet:
			item, err := s.database.PersonalAgentByID(r.Context(), userID, agentID)
			if err != nil {
				writeAgentError(w, err)
				return
			}
			writeJSON(w, http.StatusOK, item)
		case http.MethodPatch, http.MethodPut:
			var body db.PersonalAgent
			if decodeAIJSON(w, r, &body) != nil {
				return
			}
			body.ID = agentID
			if !personalAgentToolGrantsKnown(body.ToolPermissions) {
				writeAgentError(w, db.ErrSpaceInvalid)
				return
			}
			if strings.TrimSpace(body.ModelID) == "" {
				writeJSON(w, http.StatusUnprocessableEntity, map[string]string{"code": "agent_model_required"})
				return
			}
			body.ModelMode = "pinned"
			if !serveragent.GatewayModelAvailable(r.Context(), body.ModelID) {
				writeJSON(w, http.StatusUnprocessableEntity, map[string]string{"code": "agent_model_unavailable"})
				return
			}
			item, err := s.database.UpdatePersonalAgent(r.Context(), userID, body)
			if err != nil {
				writeAgentError(w, err)
				return
			}
			writeJSON(w, http.StatusOK, item)
		case http.MethodDelete:
			if err := s.database.DeletePersonalAgent(r.Context(), userID, agentID); err != nil {
				writeAgentError(w, err)
				return
			}
			w.WriteHeader(http.StatusNoContent)
		default:
			w.WriteHeader(http.StatusMethodNotAllowed)
		}
	}
}

func (s *AgentsService) PersonalAgentToolbox() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		userID, ok := s.requireUser(w, r)
		if !ok {
			return
		}
		agentID := strings.TrimSpace(chi.URLParam(r, "agentID"))
		personal, err := s.database.PersonalAgentByID(r.Context(), userID, agentID)
		if err != nil {
			writeAgentError(w, err)
			return
		}
		items := personalAgentToolboxItems(personal.ToolPermissions)
		audits, err := s.database.PersonalAgentToolboxActionAudits(r.Context(), userID, agentID, 50)
		if err != nil {
			writeAgentError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"agent": personal, "actions": items, "recent_activity": audits})
	}
}

func (s *AgentsService) PersonalAgentToolboxCatalog() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if _, ok := s.requireUser(w, r); !ok {
			return
		}
		defaults := json.RawMessage(`{"mode":"inherit_invoker","disabled_surfaces":[],"read":true,"write":true,"integrations":[]}`)
		writeJSON(w, http.StatusOK, map[string]any{"actions": personalAgentToolboxItems(defaults), "recent_activity": []db.AgentToolboxActionAudit{}})
	}
}

func personalAgentToolboxItems(policy json.RawMessage) []agentToolboxCatalogItem {
	descriptors := personalAgentToolboxCatalogDescriptors()
	items := make([]agentToolboxCatalogItem, 0, len(descriptors))
	for _, descriptor := range descriptors {
		granted := personalAgentToolPolicyAllows(policy, descriptor)
		item := agentToolboxCatalogItem{
			Name: descriptor.Name, Description: descriptor.Description, Risk: descriptor.Risk,
			Approval: descriptor.Approval, Locality: descriptor.Locality, Idempotent: descriptor.Idempotent,
			AuditEvent: descriptor.AuditEvent, RequiredPermission: descriptor.RequiredPermission,
			Granted: granted, Available: granted, Reasons: []agentToolboxAvailabilityReason{},
		}
		if !granted {
			item.Reasons = append(item.Reasons, agentToolboxAvailabilityReason{Code: "grant_required", Message: "This action is not enabled for this Agent."})
		}
		items = append(items, item)
	}
	sort.Slice(items, func(i, j int) bool { return items[i].Name < items[j].Name })
	return items
}

func personalAgentToolGrantsKnown(raw json.RawMessage) bool {
	var policy struct {
		Grants *[]db.AgentCapabilityGrant `json:"grants"`
	}
	if json.Unmarshal(raw, &policy) != nil || policy.Grants == nil {
		return true
	}
	known := map[string]string{}
	for _, descriptor := range personalAgentToolboxCatalogDescriptors() {
		known[descriptor.Name] = descriptor.Risk
	}
	seen := map[string]bool{}
	for _, grant := range *policy.Grants {
		if known[grant.Capability] != grant.Risk || seen[grant.Capability] {
			return false
		}
		seen[grant.Capability] = true
	}
	return true
}

func (s *AgentsService) PersonalAgentGrants() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		userID, ok := s.requireUser(w, r)
		if !ok {
			return
		}
		agentID := strings.TrimSpace(chi.URLParam(r, "agentID"))
		if r.Method == http.MethodGet {
			items, err := s.database.PersonalAgentGrants(r.Context(), userID, agentID)
			if err != nil {
				writeAgentError(w, err)
				return
			}
			writeJSON(w, http.StatusOK, map[string]any{"grants": items})
			return
		}
		if r.Method != http.MethodPut {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		var body struct {
			Spaces []db.PersonalAgentGrantInput `json:"spaces"`
		}
		if decodeAIJSON(w, r, &body) != nil {
			return
		}
		items, err := s.database.ReplacePersonalAgentGrants(r.Context(), userID, agentID, body.Spaces)
		if err != nil {
			writeAgentError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"grants": items})
	}
}

func (s *AgentsService) Models() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if _, ok := s.requireUser(w, r); !ok {
			return
		}
		models, err := serveragent.GatewayModels(r.Context())
		if err != nil {
			writeJSON(w, http.StatusServiceUnavailable, map[string]string{"code": "model_catalog_unavailable"})
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"catalog_version": serveragent.GatewayModelCatalogVersion, "models": models})
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
