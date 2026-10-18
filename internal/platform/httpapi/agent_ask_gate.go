package api

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"strings"

	serveragent "github.com/kannachi323/misty/server/internal/agents"
	"github.com/kannachi323/misty/server/internal/agenttools"
)

// Ask is forceful: every action that changes something pauses first. Tools
// that decide their own action and scope (connected apps, deletes, Space
// administration, schedules, screens and pages) gate themselves; every other
// change passes through this gate, which pauses in Ask and continues in Auto.

type permissionGateKey struct{}

// permissionGate returns a waiting result, or nil to let the tool run.
type permissionGate func(context.Context, agenttools.Invocation, agenttools.Descriptor, json.RawMessage) (json.RawMessage, error)

func withPermissionGate(ctx context.Context, gate permissionGate) context.Context {
	return context.WithValue(ctx, permissionGateKey{}, gate)
}

func permissionGateFrom(ctx context.Context) permissionGate {
	gate, _ := ctx.Value(permissionGateKey{}).(permissionGate)
	return gate
}

// selfGatedTools decide their own permission inside the handler.
var selfGatedTools = map[string]bool{
	appsExecuteTool: true, itemsDeleteTool: true, spacesManageTool: true, workflowUnscheduleTool: true,
	screenActTool: true, "browser.steps": true, recallSearchTool: true, recallTimelineTool: true, recallForgetTool: true, "browser.fill_form": true, "browser.skill": true,
	// Delegation hands the same mode to the child, which asks for itself.
	toolboxAgentsDelegateTasks: true, toolboxAgentsWait: true, codeDelegateTool: true, codeWaitTool: true,
}

// gatedByAsk reports whether a tool call needs the generic Ask pause.
func gatedByAsk(descriptor agenttools.Descriptor) bool {
	if descriptor.Risk == serveragent.RiskRead || descriptor.Locality == agenttools.LocalityRouted {
		return false
	}
	if selfGatedTools[descriptor.Name] || strings.HasPrefix(descriptor.Name, "log.work.") {
		return false
	}
	return true
}

// askGate pauses a change in Ask until the person approves this exact call,
// then consumes the approval. In Auto the change continues: it stays inside
// what the chat already attached.
func (s *SpacesService) askGate(ctx context.Context, invocation agenttools.Invocation, descriptor agenttools.Descriptor, arguments json.RawMessage) (json.RawMessage, error) {
	if !gatedByAsk(descriptor) {
		return nil, nil
	}
	digest := sha256.Sum256(append([]byte(descriptor.Name+"\x00"), arguments...))
	title := toolActionTitle(descriptor)
	approval, waiting, err := s.gateAction(ctx, invocation, grantNeed{action: serveragent.ActionToolWrite},
		"tool."+descriptor.Name, hex.EncodeToString(digest[:]), title, toolActionSummary(arguments))
	if err != nil || waiting != nil {
		return waiting, err
	}
	return nil, s.useAgentActionApproval(ctx, invocation.UserID, approval)
}

func toolActionTitle(descriptor agenttools.Descriptor) string {
	words := strings.Fields(strings.NewReplacer(".", " ", "_", " ").Replace(descriptor.Name))
	if len(words) == 0 {
		return "Change something"
	}
	words[0] = strings.ToUpper(words[0][:1]) + words[0][1:]
	return strings.Join(words, " ")
}

// toolActionSummary lists the call's arguments so the person sees what changes.
func toolActionSummary(arguments json.RawMessage) string {
	var fields map[string]json.RawMessage
	if json.Unmarshal(arguments, &fields) != nil {
		return ""
	}
	lines := []string{}
	for key, value := range fields {
		var text string
		if json.Unmarshal(value, &text) != nil {
			text = string(value)
		}
		lines = append(lines, key+": "+truncateAgentRuntimeText(strings.TrimSpace(text), 300))
		if len(lines) == 10 {
			break
		}
	}
	return strings.Join(lines, "\n")
}
