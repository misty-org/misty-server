package api

import (
	"encoding/json"
	"errors"
	"net/http"
	"regexp"
	"sync"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/kannachi323/misty/server/internal/agent"
	"github.com/kannachi323/misty/server/internal/platform/db"
	platformmetrics "github.com/kannachi323/misty/server/internal/platform/metrics"
	"github.com/kannachi323/misty/server/internal/workflow"
)

var (
	deviceIDPattern      = regexp.MustCompile(`^device_[0-9a-f-]{36}$`)
	deviceJobIDPattern   = regexp.MustCompile(`^devicejob_[0-9a-f-]{36}$`)
	deviceNoncePattern   = regexp.MustCompile(`^[A-Za-z0-9+/=_-]{16,200}$`)
	p2pEndpointIDPattern = regexp.MustCompile(`^[A-Za-z0-9_-]{32,128}$`)
)

const (
	deviceSignatureMaxSkew = 5 * time.Minute
	deviceSignedBodyLimit  = 2 << 20
)

// AgentsService exposes trusted-device identity, jobs and voice for global Ask.
type AgentsService struct {
	database         *db.Database
	connectedDevices ConnectedDevicesConfig
	voiceAnalyzer    *agent.SmartLibraryAnalyzer
	voiceLimiter     *SlidingWindowLimiter
	voiceMetrics     *platformmetrics.Registry
	voiceLive        VoiceLiveConfig
	deviceHub        *deviceHub
	deviceHubOnce    sync.Once
	claimWaits       deviceClaimWaits
}

func NewAgentsService(database *db.Database) *AgentsService {
	return &AgentsService{database: database, voiceLimiter: NewSlidingWindowLimiter(10, time.Minute)}
}

func (s *AgentsService) SetVoiceMetrics(metrics *platformmetrics.Registry) { s.voiceMetrics = metrics }

func (s *AgentsService) SetVoiceAnalyzer(analyzer *agent.SmartLibraryAnalyzer) {
	s.voiceAnalyzer = analyzer
}

func (s *AgentsService) ClaimWorkflowNodeJob() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		userID, ok := s.requireUser(w, r)
		if !ok {
			return
		}
		// Jobs come only from contexts whose run grant was checked, so a device
		// that is not added yet only receives work it asked for on itself.
		if _, ok := requestDevice(r); !ok {
			http.Error(w, "device signature required", http.StatusUnauthorized)
			return
		}
		deviceID := chi.URLParam(r, "deviceID")
		// Older hosts can drain legacy jobs but cannot claim begin-aware work.
		body := struct {
			ProtocolVersion int `json:"protocolVersion"`
			// WaitMs holds the claim open until a job is queued (at most 25 s).
			WaitMs int `json:"waitMs"`
		}{ProtocolVersion: 1}
		if r.ContentLength != 0 && decodeAIJSON(w, r, &body) != nil {
			http.Error(w, "invalid request", http.StatusBadRequest)
			return
		}
		if body.ProtocolVersion != 1 && body.ProtocolVersion != 2 {
			http.Error(w, "unsupported device protocol", http.StatusBadRequest)
			return
		}
		if body.WaitMs < 0 || time.Duration(body.WaitMs)*time.Millisecond > maxDeviceClaimWait {
			http.Error(w, "invalid request", http.StatusBadRequest)
			return
		}
		w.Header().Set("Cache-Control", "no-store")
		wait := time.Duration(body.WaitMs) * time.Millisecond
		if wait > 0 && !s.claimWaits.acquire(userID+":"+deviceID) {
			wait = 0
		} else if wait > 0 {
			defer s.claimWaits.release(userID + ":" + deviceID)
		}
		job, token, err := claimDeviceJobWithin(r.Context(), s.database, userID, deviceID, body.ProtocolVersion, wait)
		if r.Context().Err() != nil {
			// The device hung up while its claim was held: put the job back.
			if err == nil && job != nil {
				_ = s.database.ReleaseUnstartedWorkflowDeviceNodeJob(userID, deviceID, job.ID, token)
			}
			return
		}
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
		w.Header().Set("Cache-Control", "no-store")
		switch action {
		case "begin":
			job, err = s.database.BeginWorkflowDeviceNodeJob(userID, deviceID, jobID, body.LeaseToken)
		case "renew":
			job, err = s.database.RenewWorkflowDeviceNodeJob(userID, deviceID, jobID, body.LeaseToken)
		case "complete":
			current, lookupErr := s.database.WorkflowDeviceNodeJob(r.Context(), userID, jobID)
			if lookupErr != nil {
				writeAgentError(w, lookupErr)
				return
			}
			var schema workflow.JSONSchema
			if json.Unmarshal(current.OutputSchema, &schema) != nil || workflow.ValidateJSON(schema, body.Output) != nil {
				http.Error(w, "invalid workflow node output", http.StatusUnprocessableEntity)
				return
			}
			job, err = s.database.FinishWorkflowDeviceNodeJob(userID, deviceID, jobID, body.LeaseToken, "completed", body.Output, "")
		case "fail", "uncertain":
			if !validText(body.ErrorCode, 1, 120) {
				http.Error(w, "invalid request", http.StatusBadRequest)
				return
			}
			state := "failed"
			if action == "uncertain" {
				state = "uncertain"
			}
			job, err = s.database.FinishWorkflowDeviceNodeJob(userID, deviceID, jobID, body.LeaseToken, state, nil, body.ErrorCode)
		case "release":
			// A claim that came back after its worker stopped (a restart, a sign-in change) was
			// dropped and stayed leased until the run gave up on it, 30 s later. The device
			// puts it back instead, before it began, so the next claim takes it at once.
			if err := s.database.ReleaseUnstartedWorkflowDeviceNodeJob(userID, deviceID, jobID, body.LeaseToken); err != nil {
				writeAgentError(w, err)
				return
			}
			w.WriteHeader(http.StatusNoContent)
			return
		default:
			http.Error(w, "unsupported action", http.StatusBadRequest)
			return
		}
		writeAgentResult(w, job, err, http.StatusOK)
	}
}
