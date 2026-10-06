package agent

import (
	"context"
	"encoding/json"
	"errors"
	"log"
	"strings"

	"github.com/google/uuid"
	"github.com/kannachi323/misty/server/internal/aimodels"
	"github.com/kannachi323/misty/server/internal/modelruntime"
)

type Service struct {
	store         *SessionStore
	provider      ModelProvider
	policy        PermissionPolicy
	meter         UsageMeter
	modelResolver aimodels.Resolver
	models        *modelruntime.Client
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

func (s *Service) CompleteWithToolsForSpaceContext(ctx context.Context, userID, billingUserID, spaceID, systemPrompt, prompt string, tier AgentTier, manifest ToolManifest, execute ToolExecutor) (ToolCompletion, error) {
	spaceID = strings.TrimSpace(spaceID)
	if spaceID != "" {
		// Space work always consumes the initiating member's personal allowance;
		// the meter separately applies the owner-plan allowance to the Space.
		billingUserID = userID
	}
	if execute == nil || len(manifest.Tools) == 0 {
		// A read-only completion still needs its instructions in the provider's
		// authoritative field. Folding them into the user message loses them to
		// the persona/injection rules in buildAgentPrompt.
		tier = NormalizeAgentTier(tier)
		text, _, err := s.completeWithProviderContext(ctx, billingUserID, spaceID, systemPrompt, prompt, "automation_ai", TestingResolveAgentProvider(s.provider, tier), tier)
		return ToolCompletion{Text: text}, err
	}
	session := s.CreateSessionWithBillingAndSpace(userID, billingUserID, spaceID)
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

func (s *Service) CompleteWithTierContext(ctx context.Context, userID, prompt, meterName string, tier AgentTier) (string, UsageSettlement, error) {
	return s.CompleteWithTierForSpaceContext(ctx, userID, "", prompt, meterName, tier)
}

func (s *Service) CompleteWithTierForSpaceContext(ctx context.Context, userID, spaceID, prompt, meterName string, tier AgentTier) (string, UsageSettlement, error) {
	return s.completeWithProviderContext(ctx, userID, spaceID, "", prompt, meterName, TestingResolveAgentProvider(s.provider, NormalizeAgentTier(tier)), NormalizeAgentTier(tier))
}

func (s *Service) completeWithProviderContext(ctx context.Context, userID, spaceID, systemPrompt, prompt, meterName string, selectedProvider ModelProvider, tier AgentTier) (string, UsageSettlement, error) {
	if s.modelResolver != nil {
		config, configErr := s.modelResolver(ctx, userID, "routing")
		if configErr != nil {
			return "", UsageSettlement{}, configErr
		}
		if config != nil {
			var providerErr error
			selectedProvider, providerErr = accountCompletionProvider(s.models, config)
			if providerErr != nil {
				return "", UsageSettlement{}, providerErr
			}
		}
	}
	prompt = strings.TrimSpace(prompt)
	systemPrompt = strings.TrimSpace(systemPrompt)
	if prompt == "" {
		return "", UsageSettlement{}, ErrInvalidRequest("prompt is required")
	}
	if len(prompt)+len(systemPrompt) > MaxUserMessageBytes {
		return "", UsageSettlement{}, ErrInvalidRequest("prompt is too large")
	}
	request := ModelRequest{SessionID: uuid.NewString(), UserID: userID, SystemPrompt: systemPrompt, AgentTier: tier, Mode: ModeAsk, Messages: []Message{{Role: RoleUser, Content: prompt}}}
	provider, model := TestingProviderStatus(selectedProvider)
	idempotencyKey := "completion:" + request.SessionID
	var reservation *UsageReservation
	var err error
	if s.meter != nil && provider != ProviderMock {
		reservation, err = ReserveMeasuredUsage(s.meter, userID, idempotencyKey, provider, model, map[string]int64{"input_bytes": int64(TestingRequestSizeBytes(request)), "output_tokens": MaxModelOutputTokens}, "")
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

// WithModelRuntime sends per-request model choices (an account's own
// connection or a picked Gateway model) through the agent runtime.
func WithModelRuntime(models *modelruntime.Client) ServiceOption {
	return func(service *Service) { service.models = models }
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

func (s *Service) ProviderStatus() (string, string) {
	return s.ProviderStatusForTier(TierLow)
}

func (s *Service) ProviderStatusForTier(tier AgentTier) (string, string) {
	return TestingProviderStatus(TestingResolveAgentProvider(s.provider, tier))
}

func TestingProviderStatus(provider ModelProvider) (string, string) {
	if info, ok := provider.(ProviderInfo); ok {
		return info.ProviderName(), info.ModelName()
	}
	return ProviderMock, "mock"
}

func (s *Service) CreateSessionWithBillingAndSpace(userID, billingUserID, spaceID string) *Session {
	spaceID = strings.TrimSpace(spaceID)
	if spaceID != "" {
		billingUserID = userID
	}
	session := s.store.CreateWithBilling(userID, billingUserID)
	if spaceID == "" {
		return session
	}
	_ = s.store.WithSession(session.ID, userID, func(current *Session) error {
		current.SpaceID = spaceID
		return nil
	})
	session.SpaceID = spaceID
	return session
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
