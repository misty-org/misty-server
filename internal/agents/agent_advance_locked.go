package agent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"strings"
)

func (s *Service) advanceLocked(ctx context.Context, session *Session) error {
	if session.ProviderCallsThisTurn >= providerCallLimit(session) {
		return ErrInvalidRequest("Agent tool step limit reached; send a new message to continue")
	}
	session.ProviderCallsThisTurn++
	request := ModelRequest{
		SessionID:    session.ID,
		UserID:       session.UserID,
		SystemPrompt: session.SystemPrompt,
		AgentTier:    session.AgentTier,
		Mode:         session.Mode,
		ActiveRoot:   session.ActiveRoot,
		Messages:     append([]Message(nil), session.Messages...),
		ToolResults:  append([]ToolResult(nil), session.ToolResults...),
		Capabilities: session.Capabilities,
		KnownPaths:   knownPaths(session),
		SpaceCard:    session.SpaceCard,
		SpaceRecords: session.SpaceRecords,
		SpaceSection: session.SpaceSection,
	}
	if TestingRequestSizeBytes(request) > MaxProviderRequestBytes {
		return ErrInvalidRequest("Agent request context is too large; start a new conversation")
	}
	selectedProvider := TestingResolveAgentProvider(s.provider, session.AgentTier)
	if session.ModelID != "" {
		if !GatewayModelAvailable(ctx, session.ModelID) {
			return ErrModelUnavailable
		}
		var providerErr error
		effort := ""
		if GatewayModelSupportsReasoning(ctx, session.ModelID) {
			effort = session.ReasoningEffort
		}
		selectedProvider, providerErr = NewGatewayProviderForModelWithReasoning(session.ModelID, effort)
		if providerErr != nil {
			return providerErr
		}
	}
	provider, model := TestingProviderStatus(selectedProvider)
	idempotencyKey := fmt.Sprintf("%s:%d", session.ID, session.nextSequence+1)
	if session.BillingScope != "" {
		idempotencyKey = fmt.Sprintf("%s:%d", session.BillingScope, session.ProviderCallsThisTurn)
	}
	var reservation *UsageReservation
	var err error
	if s.meter != nil && provider != ProviderMock {
		billingUserID := session.BillingUserID
		if billingUserID == "" {
			billingUserID = session.UserID
		}
		reservation, err = s.meter.Reserve(billingUserID, idempotencyKey, hostedAIMeterAgent, provider, model, estimateRequestTokens(request), MaxModelOutputTokens)
		if err != nil {
			return err
		}
	}
	response, err := nextProvider(ctx, selectedProvider, request)
	if err != nil {
		if reservation != nil {
			_ = s.meter.Release(reservation)
		}
		if errors.Is(err, context.Canceled) {
			return err
		}
		log.Printf("agent provider request failed for tier %s: %v", session.AgentTier, err)
		session.appendEvent(AgentEvent{Type: EventError, Message: "The agent could not complete this request."})
		return nil
	}
	response.Citations = TestingGroundedAgentCitations(request, response.Citations)
	settlement := UsageSettlement{}
	if reservation != nil {
		settlement, err = s.meter.Settle(reservation, idempotencyKey+":settle", hostedAIMeterAgent, provider, model, response.Usage)
		if err != nil {
			_ = s.meter.Release(reservation)
			return err
		}
	}
	if strings.TrimSpace(response.Text) != "" {
		session.Messages = append(session.Messages, Message{Role: RoleAgent, Content: response.Text})
		resetAt := settlement.ResetAt
		session.appendEvent(AgentEvent{Type: EventAgentMessage, Text: response.Text, Citations: response.Citations, HostedAIUsedRatio: settlement.UsedRatio, HostedAIResetAt: &resetAt})
	}
	if len(response.ToolRequests) > 0 {
		if session.ProviderCallsThisTurn >= providerCallLimit(session) {
			clear(session.PendingToolRequests)
			session.appendEvent(AgentEvent{Type: EventError, Message: "The agent reached the tool step limit. Send a new message to continue."})
			return nil
		}
		requests, rejected := TestingAuthorizeToolRequests(session.Capabilities, response.ToolRequests)
		if !session.AllowTools {
			rejected += len(requests)
			requests = nil
		}
		if !session.AllowWriteTools {
			filtered := requests[:0]
			for _, request := range requests {
				if request.Risk == RiskRead {
					filtered = append(filtered, request)
				} else {
					rejected++
				}
			}
			requests = filtered
		}
		if rejected > 0 {
			if reservation != nil && settlement.ChargedMicrousd > 0 {
				if _, refundErr := s.meter.Refund(reservation, idempotencyKey+":capability-refund", "capability_envelope_rejected"); refundErr != nil {
					log.Printf("could not refund rejected agent capability call: %v", refundErr)
				}
			}
			session.appendEvent(AgentEvent{Type: EventError, Message: "The agent requested a tool outside the allowed capability envelope."})
		}
		requests = s.policy.Apply(session.Mode, requests)
		if len(requests) > MaxToolResultsPerRequest {
			requests = requests[:MaxToolResultsPerRequest]
		}
		clear(session.PendingToolRequests)
		for _, request := range requests {
			session.PendingToolRequests[request.ID] = request.Name
		}
		session.appendEvent(AgentEvent{Type: EventToolRequest, ToolRequests: requests})
	} else {
		clear(session.PendingToolRequests)
	}
	if response.FilePlan != nil {
		problems := ValidateFilePlan(*response.FilePlan, PlanValidationContext{KnownPaths: knownPaths(session)})
		if len(problems) > 0 {
			plan := *response.FilePlan
			plan.Warnings = append(plan.Warnings, problems...)
			session.appendEvent(AgentEvent{Type: EventFilePlan, FilePlan: &plan})
			return nil
		}
		session.appendEvent(AgentEvent{Type: EventFilePlan, FilePlan: response.FilePlan})
	}
	return nil
}

func TestingAuthorizeToolRequests(manifest ToolManifest, requests []ToolRequest) ([]ToolRequest, int) {
	allowed := make(map[string]ToolDefinition, len(manifest.Tools))
	for _, definition := range manifest.Tools {
		definition.Name = strings.TrimSpace(definition.Name)
		if definition.Name != "" {
			allowed[definition.Name] = definition
		}
	}
	authorized := make([]ToolRequest, 0, len(requests))
	rejected := 0
	for _, request := range requests {
		definition, ok := allowed[request.Name]
		if len(request.Arguments) == 0 {
			request.Arguments = json.RawMessage(`{}`)
		}
		if !ok || !validToolArguments(request.Arguments) {
			rejected++
			continue
		}
		// The published/provider manifest is authoritative. A model cannot
		// downgrade a write or destructive request by labeling it as a read.
		request.Risk = normalizeRisk(definition.Risk)
		authorized = append(authorized, request)
	}
	return authorized, rejected
}

func validToolArguments(raw json.RawMessage) bool {
	if len(raw) == 0 || len(raw) > 256<<10 {
		return false
	}
	var object map[string]any
	return json.Unmarshal(raw, &object) == nil && object != nil
}

func containsPreviewFileResult(results []ToolResult) bool {
	for _, result := range results {
		if result.Name == ToolPreviewFile && result.OK {
			return true
		}
	}
	return false
}

func providerCallLimit(session *Session) int {
	if strings.HasPrefix(session.BillingScope, "agent-job:") {
		for _, tool := range session.Capabilities.Tools {
			if tool.Name == ToolPreviewFile {
				return MaxDocumentCallsPerTurn
			}
		}
	}
	return MaxProviderCallsPerTurn
}

func estimateRequestTokens(request ModelRequest) int64 {
	characters := TestingRequestSizeBytes(request)
	return int64(characters/4 + 256)
}

func TestingRequestSizeBytes(request ModelRequest) int {
	characters := 0
	// The system prompt and the Space blocks are part of the payload the provider
	// is sent, so they have to be counted here. Omitting them made the
	// MaxProviderRequestBytes guard blind to context size, and made
	// estimateRequestTokens under-reserve credits by the whole prompt.
	characters += len(request.SystemPrompt) + len(request.SpaceCard) + len(request.SpaceRecords)
	for _, message := range request.Messages {
		characters += len(message.Content)
	}
	for _, result := range request.ToolResults {
		characters += len(result.Result) + len(result.Error)
	}
	characters += len(request.ActiveRoot)
	for _, path := range request.KnownPaths {
		characters += len(path)
	}
	for _, tool := range request.Capabilities.Tools {
		characters += len(tool.Name) + len(tool.Description) + len(tool.Risk) + len(tool.InputSchema)
	}
	return characters
}

func knownPaths(session *Session) []string {
	paths := make([]string, 0, len(session.KnownPaths))
	for path := range session.KnownPaths {
		paths = append(paths, path)
	}
	return paths
}

func collectKnownPaths(session *Session, results []ToolResult) {
	for _, result := range results {
		if !result.OK || len(result.Result) == 0 {
			continue
		}
		var payload any
		if err := json.Unmarshal(result.Result, &payload); err != nil {
			continue
		}
		collectKnownPathsFromValue(session, payload)
	}
}
