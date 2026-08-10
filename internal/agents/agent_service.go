package agent

import (
	"context"
	"encoding/json"
	"errors"
	"log"
	"strings"

	"github.com/google/uuid"
)

type Service struct {
	store    *SessionStore
	provider ModelProvider
	policy   PermissionPolicy
	meter    UsageMeter
}

type ToolExecutor func(context.Context, ToolRequest) (json.RawMessage, error)

type ToolCompletion struct {
	Text      string
	Citations []AgentCitation
	ToolCalls int
}

// MaxToolRounds bounds how many tool round trips one automated run may make.
// Generous for real work, finite so a runaway loop cannot bill forever.
const MaxToolRounds = 12

// ErrToolRoundLimit is returned when a run exceeds MaxToolRounds.
var ErrToolRoundLimit = errors.New("agent run exceeded its tool round limit")

// CompleteWithToolsContext runs the same agent session/tool protocol used by
// interactive chat, but dispatches each manifest-authorized request through a
// server-owned executor. This is the bridge used by automated Agent tasks;
// finite provider/tool limits remain enforced by the Session service.
//
// systemPrompt carries the selected Agent's identity and approved instructions.
// It must not be folded into the user message: the prompt builder surfaces it
// as agent_instructions_and_context, and the persona rule refuses to adopt any
// identity that does not arrive through that field. Callers with no Agent
// identity of their own pass an empty string.
func (s *Service) CompleteWithToolsContext(ctx context.Context, userID, billingUserID, systemPrompt, prompt string, tier AgentTier, manifest ToolManifest, execute ToolExecutor) (ToolCompletion, error) {
	if execute == nil || len(manifest.Tools) == 0 {
		// The plain completion path has no session to carry the identity, so it
		// is the one place the two prompts have to travel together.
		merged := strings.TrimSpace(strings.TrimSpace(systemPrompt) + "\n\n" + prompt)
		text, _, err := s.CompleteWithTierContext(ctx, billingUserID, merged, "automation_ai", tier)
		return ToolCompletion{Text: text}, err
	}
	session := s.CreateSessionWithBilling(userID, billingUserID)
	defer func() { _ = s.Forget(session.ID, userID) }()
	if err := s.SetSessionSystemPrompt(session.ID, userID, systemPrompt); err != nil {
		return ToolCompletion{}, err
	}
	if err := s.SendMessageWithTierContext(ctx, session.ID, userID, AgentMessageRequest{Mode: ModeFull, UserMessage: prompt, Capabilities: manifest}, tier); err != nil {
		return ToolCompletion{}, err
	}
	after := int64(0)
	completion := ToolCompletion{}
	// Each round trip is another paid model call. Without a cap, a model that
	// keeps asking for tools loops until the caller disconnects, so one request
	// can bill indefinitely.
	for round := 0; ; round++ {
		if round >= MaxToolRounds {
			return ToolCompletion{}, ErrToolRoundLimit
		}
		if ctx.Err() != nil {
			return ToolCompletion{}, ctx.Err()
		}
		events, err := s.Events(session.ID, userID, after)
		if err != nil {
			return ToolCompletion{}, err
		}
		requests := []ToolRequest{}
		for _, event := range events {
			if event.Sequence > after {
				after = event.Sequence
			}
			switch event.Type {
			case EventError:
				return ToolCompletion{}, errors.New(event.Message)
			case EventAgentMessage:
				if strings.TrimSpace(event.Text) != "" {
					completion.Text = strings.TrimSpace(event.Text)
					completion.Citations = append([]AgentCitation(nil), event.Citations...)
				}
			case EventToolRequest:
				requests = append(requests, event.ToolRequests...)
			}
		}
		if len(requests) == 0 {
			if completion.Text == "" {
				return ToolCompletion{}, errors.New("the agent returned neither a result nor a tool request")
			}
			return completion, nil
		}
		results := make([]ToolResult, 0, len(requests))
		for _, request := range requests {
			completion.ToolCalls++
			result, executeErr := execute(ctx, request)
			item := ToolResult{RequestID: request.ID, Name: request.Name, OK: executeErr == nil, Result: result}
			if executeErr != nil {
				item.Error = executeErr.Error()
			}
			results = append(results, item)
		}
		if err := s.SubmitToolResultsWithTierContext(ctx, session.ID, userID, results, tier); err != nil {
			return ToolCompletion{}, err
		}
	}
}

// CompleteWithModelToolsContext executes a bounded tool run using an explicitly
// pinned gateway model. Agent memberships pin immutable profile versions, so a
// Space run must not silently fall back to the service's default provider.
func (s *Service) CompleteWithModelToolsContext(ctx context.Context, userID, billingUserID, systemPrompt, prompt, modelID string, tier AgentTier, manifest ToolManifest, execute ToolExecutor) (ToolCompletion, error) {
	if !GatewayModelAvailable(ctx, modelID) {
		return ToolCompletion{}, ErrModelUnavailable
	}
	provider, err := NewGatewayProviderForModel(modelID)
	if err != nil {
		return ToolCompletion{}, err
	}
	selected := &Service{store: s.store, provider: provider, policy: s.policy, meter: s.meter}
	return selected.CompleteWithToolsContext(ctx, userID, billingUserID, systemPrompt, prompt, tier, manifest, execute)
}

func (s *Service) Complete(userID, prompt, meterName string) (string, UsageSettlement, error) {
	return s.CompleteWithTier(userID, prompt, meterName, TierLow)
}

func (s *Service) CompleteWithTier(userID, prompt, meterName string, tier AgentTier) (string, UsageSettlement, error) {
	return s.CompleteWithTierContext(context.Background(), userID, prompt, meterName, tier)
}

func (s *Service) CompleteWithTierContext(ctx context.Context, userID, prompt, meterName string, tier AgentTier) (string, UsageSettlement, error) {
	return s.completeWithProviderContext(ctx, userID, prompt, meterName, TestingResolveAgentProvider(s.provider, NormalizeAgentTier(tier)), NormalizeAgentTier(tier))
}

func (s *Service) CompleteWithModelContext(ctx context.Context, userID, prompt, meterName, modelID string) (string, UsageSettlement, error) {
	if !GatewayModelAvailable(ctx, modelID) {
		return "", UsageSettlement{}, ErrModelUnavailable
	}
	provider, err := NewGatewayProviderForModel(modelID)
	if err != nil {
		return "", UsageSettlement{}, err
	}
	return s.completeWithProviderContext(ctx, userID, prompt, meterName, provider, TierLow)
}

func (s *Service) completeWithProviderContext(ctx context.Context, userID, prompt, meterName string, selectedProvider ModelProvider, tier AgentTier) (string, UsageSettlement, error) {
	prompt = strings.TrimSpace(prompt)
	if prompt == "" {
		return "", UsageSettlement{}, ErrInvalidRequest("prompt is required")
	}
	if len(prompt) > MaxUserMessageBytes {
		return "", UsageSettlement{}, ErrInvalidRequest("prompt is too large")
	}
	request := ModelRequest{SessionID: uuid.NewString(), UserID: userID, AgentTier: tier, Mode: ModeAsk, Messages: []Message{{Role: RoleUser, Content: prompt}}}
	provider, model := TestingProviderStatus(selectedProvider)
	idempotencyKey := "completion:" + request.SessionID
	var reservation *UsageReservation
	var err error
	if s.meter != nil && provider != ProviderMock {
		reservation, err = s.meter.Reserve(userID, idempotencyKey, meterName, provider, model, estimateRequestTokens(request), MaxModelOutputTokens)
		if err != nil {
			return "", UsageSettlement{}, err
		}
	}
	response, err := nextProvider(ctx, selectedProvider, request)
	if err != nil {
		if reservation != nil {
			_ = s.meter.Release(reservation)
		}
		if !errors.Is(err, context.Canceled) {
			log.Printf("agent completion provider request failed for tier %s: %v", tier, err)
		}
		return "", UsageSettlement{}, err
	}
	settlement := UsageSettlement{}
	if reservation != nil {
		settlement, err = s.meter.Settle(reservation, idempotencyKey+":settle", meterName, provider, model, response.Usage)
		if err != nil {
			_ = s.meter.Release(reservation)
			return "", UsageSettlement{}, err
		}
	}
	return response.Text, settlement, nil
}

type ServiceOption func(*Service)

func WithUsageMeter(meter UsageMeter) ServiceOption {
	return func(service *Service) { service.meter = meter }
}

func NewService(store *SessionStore, provider ModelProvider, options ...ServiceOption) *Service {
	if store == nil {
		store = NewSessionStore(0)
	}
	if provider == nil {
		provider = MockProvider{}
	}
	service := &Service{
		store:    store,
		provider: provider,
		policy:   PermissionPolicy{},
	}
	for _, option := range options {
		option(service)
	}
	return service
}

func (s *Service) Store() *SessionStore {
	return s.store
}

func (s *Service) ProviderStatus() (string, string) {
	return s.ProviderStatusForTier(TierLow)
}

func (s *Service) ProviderStatusForTier(tier AgentTier) (string, string) {
	return TestingProviderStatus(TestingResolveAgentProvider(s.provider, tier))
}

func (s *Service) AgentConfigured(tier AgentTier) bool {
	provider, _ := s.ProviderStatusForTier(tier)
	return provider != ProviderMock
}

func TestingProviderStatus(provider ModelProvider) (string, string) {
	if info, ok := provider.(ProviderInfo); ok {
		return info.ProviderName(), info.ModelName()
	}
	return ProviderMock, "mock"
}

func (s *Service) CreateSession(userID string) *Session {
	return s.store.Create(userID)
}

func (s *Service) CreateSessionWithBilling(userID, billingUserID string) *Session {
	return s.store.CreateWithBilling(userID, billingUserID)
}

func (s *Service) CreateSessionWithModel(userID, billingUserID, modelID string) *Session {
	return s.store.CreateWithModel(userID, billingUserID, modelID)
}

func (s *Service) ConfigureSession(sessionID, userID, systemPrompt string, allowTools, allowWriteTools bool) error {
	return s.store.WithSession(sessionID, userID, func(session *Session) error {
		session.SystemPrompt = strings.TrimSpace(systemPrompt)
		session.AllowTools = allowTools
		session.AllowWriteTools = allowWriteTools
		return nil
	})
}

// SetSessionSystemPrompt supplies the Agent identity and approved instructions
// the prompt builder emits as agent_instructions_and_context. Unlike
// ConfigureSession it leaves the tool flags alone, so a caller can name the
// Agent without also restating its tool policy.
func (s *Service) SetSessionSystemPrompt(sessionID, userID, systemPrompt string) error {
	return s.store.WithSession(sessionID, userID, func(session *Session) error {
		session.SystemPrompt = strings.TrimSpace(systemPrompt)
		return nil
	})
}

// SetSessionReasoningEffort pins the reasoning effort ("low"/"medium"/"high") for
// a session. It is only forwarded to the gateway for reasoning-capable models.
func (s *Service) SetSessionReasoningEffort(sessionID, userID, effort string) error {
	return s.store.WithSession(sessionID, userID, func(session *Session) error {
		session.ReasoningEffort = strings.TrimSpace(effort)
		return nil
	})
}

func (s *Service) CreateSessionForJob(userID, billingUserID, jobID string) *Session {
	return s.store.CreateWithBillingScope(userID, billingUserID, "agent-job:"+jobID)
}
