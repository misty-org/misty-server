package api

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	serveragent "github.com/kannachi323/misty/server/internal/agents"
	"github.com/kannachi323/misty/server/internal/agenttools"
	db "github.com/kannachi323/misty/server/internal/platform/postgres"
	workflowv2 "github.com/kannachi323/misty/server/internal/workflows"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

type mcpRuntimeAccess struct {
	claims   mcpAccessClaims
	run      *db.SpaceRun
	record   *db.AIInvocationRecord
	prepared *preparedAIInvocationRuntime
}

type mcpRuntimeAccessContextKey struct{}

func (s *SpacesService) MistyMCP() http.Handler {
	runtimeLimiter := NewSlidingWindowLimiter(240, time.Minute)
	streamable := mcp.NewStreamableHTTPHandler(func(r *http.Request) *mcp.Server {
		access, ok := r.Context().Value(mcpRuntimeAccessContextKey{}).(*mcpRuntimeAccess)
		if !ok || access == nil {
			return nil
		}
		return s.mcpServerForRuntime(r.Context(), access)
	}, &mcp.StreamableHTTPOptions{
		Stateless:                    true,
		JSONResponse:                 true,
		MaxRequestBodyBytes:          1 << 20,
		PropagateRequestCancellation: true,
	})
	protected := http.NewCrossOriginProtection().Handler(streamable)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		claims, err := s.authenticateMCPRuntimeRequest(r)
		if err != nil {
			w.Header().Set("WWW-Authenticate", `Bearer realm="misty-mcp"`)
			writeJSON(w, http.StatusUnauthorized, map[string]string{"code": "invalid_mcp_token"})
			return
		}
		rateKey := strings.Join([]string{
			claims.Subject,
			claims.RunID,
			claims.RuntimeRunID,
		}, "\x00")
		if allowed, retryAfter := runtimeLimiter.Allow(rateKey, time.Now()); !allowed {
			w.Header().Set("Retry-After", strconv.Itoa(retryAfterSeconds(retryAfter)))
			writeJSON(w, http.StatusTooManyRequests, map[string]string{
				"code":    "mcp_rate_limited",
				"message": "This agent run is making too many tool requests. Please retry shortly.",
			})
			return
		}
		access, err := s.authorizeMCPRuntimeClaims(r, claims)
		if err != nil {
			w.Header().Set("WWW-Authenticate", `Bearer realm="misty-mcp"`)
			writeJSON(w, http.StatusUnauthorized, map[string]string{"code": "invalid_mcp_token"})
			return
		}
		protected.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), mcpRuntimeAccessContextKey{}, access)))
	})
}

func (s *SpacesService) authenticateMCPRuntimeRequest(r *http.Request) (mcpAccessClaims, error) {
	token, ok := TestingBearerTokenFromRequest(r)
	if !ok {
		return mcpAccessClaims{}, errMCPAccessDenied
	}
	claims, err := verifyMCPAccessToken(token, s.agentRuntime.secret, s.agentRuntime.previousSecret)
	if err != nil {
		return mcpAccessClaims{}, err
	}
	return claims, nil
}

func (s *SpacesService) authorizeMCPRuntimeClaims(r *http.Request, claims mcpAccessClaims) (*mcpRuntimeAccess, error) {
	access := &mcpRuntimeAccess{claims: claims}
	if isAIInvocationRuntimeID(claims.RunID) {
		record, err := s.database.ValidateAIInvocationRuntime(r.Context(), claims.RunID, claims.RuntimeRunID)
		if err != nil || record.UserID != claims.Subject {
			return nil, errMCPAccessDenied
		}
		prepared, err := s.prepareAIInvocationRuntime(r.Context(), record)
		if err != nil {
			return nil, errMCPAccessDenied
		}
		access.record, access.prepared = record, prepared
		return access, nil
	}
	run, _, err := s.database.ValidatePersonalAgentTaskRuntime(r.Context(), claims.RunID, claims.RuntimeRunID, true)
	if err != nil || run.OwnerUserID != claims.Subject {
		return nil, errMCPAccessDenied
	}
	access.run = run
	return access, nil
}

func (s *SpacesService) mcpServerForRuntime(ctx context.Context, access *mcpRuntimeAccess) *mcp.Server {
	server := mcp.NewServer(&mcp.Implementation{
		Name: "misty", Title: "Misty", Version: "1.0.0",
		Description: "Run-scoped access to Misty's permissioned application tools.",
	}, nil)
	if access.run != nil {
		toolbox, invocation, authorize, err := s.resolvePersonalAgentRuntimeToolbox(ctx, access.run)
		if err != nil {
			return server
		}
		for _, descriptor := range allowedMCPDescriptors(ctx, toolbox, invocation, authorize) {
			if descriptor.Name == "browser.request_user_action" && !access.claims.InterventionWaits {
				continue
			}
			descriptor := descriptor
			definition := mcpRunToolDefinition(descriptor)
			server.AddTool(definition, func(toolCtx context.Context, request *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
				return s.callPersonalAgentMCPTool(toolCtx, access, descriptor, request)
			})
		}
		return server
	}
	// Chat runs list exactly their resolved catalog; every call re-authorizes.
	for _, descriptor := range access.prepared.toolbox.Descriptors() {
		if !agentToolNameAllowed(access.prepared.allowedTools, descriptor.Name) || descriptor.Name == "browser.request_user_action" && !access.claims.InterventionWaits {
			continue
		}
		descriptor := descriptor
		server.AddTool(mcpToolDefinition(descriptor), func(toolCtx context.Context, request *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			return s.callAIInvocationMCPTool(toolCtx, access, descriptor, request)
		})
	}
	return server
}

func allowedMCPDescriptors(ctx context.Context, toolbox *agenttools.Registry, invocation agenttools.Invocation, authorize agenttools.Authorizer) []agenttools.Descriptor {
	allowed := []agenttools.Descriptor{}
	for _, descriptor := range toolbox.Descriptors() {
		manifest, err := toolbox.Resolve(ctx, invocation, []string{descriptor.Name}, authorize)
		if err == nil && len(manifest.Tools) == 1 {
			allowed = append(allowed, descriptor)
		}
	}
	return allowed
}

func mcpToolDefinition(descriptor agenttools.Descriptor) *mcp.Tool {
	readOnly := descriptor.Risk == serveragent.RiskRead
	destructive := descriptor.Risk == serveragent.RiskDangerous
	openWorld := descriptor.Locality == agenttools.LocalityProvider || strings.HasPrefix(descriptor.Name, "browser.")
	return &mcp.Tool{
		Name: descriptor.Name, Title: descriptor.Name, Description: descriptor.Description,
		InputSchema: descriptor.InputSchema, OutputSchema: descriptor.OutputSchema,
		Annotations: &mcp.ToolAnnotations{
			ReadOnlyHint: readOnly, IdempotentHint: descriptor.Idempotent,
			DestructiveHint: &destructive, OpenWorldHint: &openWorld,
		},
		Meta: mcp.Meta{
			"misty/risk": descriptor.Risk, "misty/approval": string(descriptor.Approval),
			"misty/locality": string(descriptor.Locality), "misty/version": descriptor.Version,
		},
	}
}

func (s *SpacesService) callPersonalAgentMCPTool(ctx context.Context, access *mcpRuntimeAccess, descriptor agenttools.Descriptor, request *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	// A lost wait response may be replayed while paused, but a pending wait
	// never becomes permission to execute another tool from the same runtime.
	if access.run.State == "awaiting_intervention" && descriptor.Name != "browser.request_user_action" {
		return mcpToolError(db.ErrSpaceForbidden), nil
	}
	call := mcpRuntimeToolCall(access.claims.RuntimeRunID, descriptor.Name, request)
	call.SupportsIntervention = access.claims.InterventionWaits
	outcome, err := s.executePersonalAgentRuntimeTool(ctx, access.run, call)
	if err != nil {
		var intervention *aiInterventionRequired
		if errors.As(err, &intervention) {
			return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: "User action is required in the original browser target."}}, Meta: mcp.Meta{"misty/intervention_wait": intervention.wait}}, nil
		}
		return mcpToolError(err), nil
	}
	if outcome.Approval != nil {
		return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: "Creator approval is required before this action can continue."}}, Meta: mcp.Meta{"misty/approval": outcome.Approval}}, nil
	}
	if outcome.DeviceWait {
		return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: "The attached device is currently unavailable."}}, Meta: mcp.Meta{"misty/device_wait": true}}, nil
	}
	if len(outcome.ProviderOutcome) > 0 {
		return mcpStructuredResult(outcome.ProviderOutcome), nil
	}
	return mcpStructuredResult(outcome.Result), nil
}

func (s *SpacesService) callAIInvocationMCPTool(ctx context.Context, access *mcpRuntimeAccess, descriptor agenttools.Descriptor, request *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	call := mcpRuntimeToolCall(access.claims.RuntimeRunID, descriptor.Name, request)
	result, err := s.executeAIInvocationMCPTool(ctx, access, call)
	if err != nil {
		var intervention *aiInterventionRequired
		if errors.As(err, &intervention) {
			return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: "User action is required in the original browser target."}}, Meta: mcp.Meta{"misty/intervention_wait": intervention.wait}}, nil
		}
		if errors.Is(err, errAIInvocationDeviceWait) {
			return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: "Waiting for the original attached browser device."}}, Meta: mcp.Meta{"misty/device_wait": true}}, nil
		}
		return mcpToolError(err), nil
	}
	return mcpStructuredResult(result), nil
}

func mcpRuntimeToolCall(runtimeRunID, name string, request *mcp.CallToolRequest) agentRuntimeToolCall {
	call := agentRuntimeToolCall{RuntimeRunID: runtimeRunID, CallID: randomMCPCallID(), Name: name, Arguments: json.RawMessage(`{}`)}
	if request == nil || request.Params == nil {
		return call
	}
	if len(request.Params.Arguments) > 0 {
		call.Arguments = append(json.RawMessage(nil), request.Params.Arguments...)
	}
	call.CallID = mcpMetaString(request.Params.Meta, "misty/call_id", call.CallID)
	call.DeviceHookToken = mcpMetaString(request.Params.Meta, "misty/device_hook_token", "")
	return call
}

func mcpMetaString(meta mcp.Meta, key, fallback string) string {
	value, _ := meta[key].(string)
	value = strings.TrimSpace(value)
	if value == "" || len(value) > 500 {
		return fallback
	}
	return value
}

func randomMCPCallID() string {
	var value [18]byte
	if _, err := rand.Read(value[:]); err != nil {
		return "mcp-call"
	}
	return "mcp_" + base64.RawURLEncoding.EncodeToString(value[:])
}

func mcpStructuredResult(raw json.RawMessage) *mcp.CallToolResult {
	var structured any
	if len(raw) == 0 || json.Unmarshal(raw, &structured) != nil {
		return &mcp.CallToolResult{IsError: true, Content: []mcp.Content{&mcp.TextContent{Text: "The action returned no valid result. Its completion is unconfirmed."}}}
	}
	return &mcp.CallToolResult{
		Content:           []mcp.Content{&mcp.TextContent{Text: string(raw)}},
		StructuredContent: structured,
	}
}

func mcpToolError(err error) *mcp.CallToolResult {
	if errors.Is(err, db.ErrAgentToolboxActionUnknown) {
		return mcpStructuredResult(json.RawMessage(`{"status":"uncertain","reason":"The action may have completed. Reconcile its outcome before retrying."}`))
	}
	if errors.Is(err, errWorkspaceSnapshotStale) {
		return mcpStructuredResult(json.RawMessage(`{"status":"failure","reason":"browser_snapshot_stale","attempted":false,"message":"The screenshot was consumed or became stale. No input was dispatched. Call browser_workspace_visual to capture the current control surface, then decide the next action with its new documentId. Capture again after every action, including clicking before typing."}`))
	}
	if errors.Is(err, errBrowserSnapshotStale) {
		return mcpStructuredResult(json.RawMessage(`{"status":"failure","reason":"browser_snapshot_stale","attempted":false,"message":"The page changed or the inspection was already consumed. No action was dispatched. Call browser_inspect again and use its new references before deciding the next action."}`))
	}
	message := "Misty could not complete this tool call."
	var invalid serveragent.ErrInvalidRequest
	if errors.Is(err, errBrowserObservationFailed) {
		message = "browser_observation_failed: The page could not be observed; the agent window may be paused or closed. Observe it again once; if that also fails, report the browser problem."
	} else if errors.Is(err, errBrowserWebviewUnavailable) {
		message = "browser_webview_unavailable: The local browser view is unavailable. Reopen the browser view and retry the task."
	} else if errors.Is(err, errDesktopAccessibilityRequired) {
		message = "desktop_accessibility_required: Allow the running Misty app in System Settings → Privacy & Security → Accessibility, then retry."
	} else if errors.Is(err, errDesktopScreenRecordingRequired) {
		message = "desktop_screen_recording_required: Allow the running Misty app in System Settings → Privacy & Security → Screen Recording, then retry."
	} else if errors.Is(err, db.ErrAgentExecutionTimeLimit) {
		message = "agent_execution_time_limit: This run used its execution-time allowance. Review completed work before starting another request."
	} else if errors.Is(err, agenttools.ErrCapabilityDenied) || errors.Is(err, agenttools.ErrToolNotFound) || errors.Is(err, agenttools.ErrApprovalRequired) || errors.Is(err, workflowv2.ErrCapabilityDenied) || errors.Is(err, db.ErrSpaceForbidden) {
		message = "This tool call is not allowed for the current run."
	} else if errors.As(err, &invalid) {
		// Validation messages tell the model how to correct its call.
		message = truncateAgentRuntimeText(string(invalid), 500)
	} else if errors.Is(err, agenttools.ErrArgumentsInvalid) {
		message = "The arguments do not match this tool's input schema."
	} else if errors.Is(err, db.ErrSpaceInvalid) {
		message = "The tool arguments are invalid."
	} else if strings.Contains(err.Error(), "drawing_conflict") {
		message = "The drawing changed since it was read. Call drawings_read again, then retry with its latest base_hash."
	} else if strings.Contains(err.Error(), "document_too_large") {
		message = "The drawing would exceed the collaboration document size limit. Apply a smaller scene or delete unused elements first."
	}
	return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: message}}, IsError: true}
}
