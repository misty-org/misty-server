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
// Generous for real work, finite so a runaway loop cannot run forever.
const MaxToolRounds = 12

// ErrToolRoundLimit is returned when a run exceeds MaxToolRounds.
var ErrToolRoundLimit = errors.New("agent run exceeded its tool round limit")

func (s *Service) CompleteWithToolsForSpaceContext(ctx context.Context, userID, modelUserID, spaceID, systemPrompt, prompt string, manifest ToolManifest, execute ToolExecutor) (ToolCompletion, error) {
	spaceID = strings.TrimSpace(spaceID)
	if spaceID != "" || modelUserID == "" {
		// Space work always runs on the initiating member's own models.
		modelUserID = userID
	}
	if execute == nil || len(manifest.Tools) == 0 {
		// A read-only completion still needs its instructions in the provider's
		// authoritative field. Folding them into the user message loses them to
		// the persona/injection rules in buildAgentPrompt.
		text, err := s.completeWithProviderContext(ctx, modelUserID, systemPrompt, prompt)
		return ToolCompletion{Text: text}, err
	}
	session := s.CreateSessionWithSpace(userID, spaceID)
	defer func() { _ = s.Forget(session.ID, userID) }()
	if err := s.SetSessionSystemPrompt(session.ID, userID, systemPrompt); err != nil {
		return ToolCompletion{}, err
	}
	if err := s.SendMessageContext(ctx, session.ID, userID, AgentMessageRequest{Mode: ModeFull, UserMessage: prompt, Capabilities: manifest}); err != nil {
		return ToolCompletion{}, err
	}
	after := int64(0)
	completion := ToolCompletion{}
	// Each round trip is another model call. Without a cap, a model that
	// keeps asking for tools loops until the caller disconnects.
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
		if err := s.SubmitToolResultsContext(ctx, session.ID, userID, results); err != nil {
			return ToolCompletion{}, err
		}
	}
}

func (s *Service) CompleteContext(ctx context.Context, userID, prompt string) (string, error) {
	return s.completeWithProviderContext(ctx, userID, "", prompt)
}

func (s *Service) completeWithProviderContext(ctx context.Context, userID, systemPrompt, prompt string) (string, error) {
	selectedProvider := s.provider
	if s.modelResolver != nil {
		config, configErr := s.modelResolver(ctx, userID, "routing")
		if configErr != nil {
			return "", configErr
		}
		if config != nil {
			var providerErr error
			selectedProvider, providerErr = accountCompletionProvider(s.models, config)
			if providerErr != nil {
				return "", providerErr
			}
		}
	}
	prompt = strings.TrimSpace(prompt)
	systemPrompt = strings.TrimSpace(systemPrompt)
	if prompt == "" {
		return "", ErrInvalidRequest("prompt is required")
	}
	if len(prompt)+len(systemPrompt) > MaxUserMessageBytes {
		return "", ErrInvalidRequest("prompt is too large")
	}
	request := ModelRequest{SessionID: uuid.NewString(), UserID: userID, SystemPrompt: systemPrompt, Mode: ModeAsk, Messages: []Message{{Role: RoleUser, Content: prompt}}}
	response, err := nextProvider(ctx, selectedProvider, request)
	if err != nil {
		if !errors.Is(err, context.Canceled) {
			log.Printf("agent completion provider request failed: %v", err)
		}
		return "", err
	}
	return response.Text, nil
}

type ServiceOption func(*Service)

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
	return ProviderStatus(s.provider)
}

func ProviderStatus(provider ModelProvider) (string, string) {
	if info, ok := provider.(ProviderInfo); ok {
		return info.ProviderName(), info.ModelName()
	}
	return ProviderMock, "mock"
}

func (s *Service) CreateSessionWithSpace(userID, spaceID string) *Session {
	spaceID = strings.TrimSpace(spaceID)
	session := s.store.Create(userID)
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
