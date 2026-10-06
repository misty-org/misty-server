package api

import (
	"encoding/json"
	"strings"

	db "github.com/kannachi323/misty/server/internal/platform/postgres"
)

// What an agent sees about a request it sent: A2A-style state and the reply.
func agentRequestFinished(runState string) bool {
	state := agentRequestState(runState)
	return state == "completed" || state == "failed" || state == "canceled"
}

// agentRequestState maps run states onto the A2A task states.
func agentRequestState(runState string) string {
	switch runState {
	case "queued":
		return "submitted"
	case "completed", "completed_with_errors":
		return "completed"
	case "failed", "rejected":
		return "failed"
	case "canceled":
		return "canceled"
	default:
		return "working"
	}
}

// memberRequestState is agentRequestState plus the wait for the target
// owner's approval, which is A2A's auth-required.
func memberRequestState(request *db.AgentMemberRequest) string {
	if request.Approval == "pending" && request.RunState == "queued" {
		return "awaiting_approval"
	}
	return agentRequestState(request.RunState)
}

func agentRequestView(request *db.AgentMemberRequest) json.RawMessage {
	state := memberRequestState(request)
	view := map[string]any{
		"request_id": request.ID, "state": state, "agent": request.TargetAgentName, "owner": request.TargetOwnerName, "space": request.SpaceName,
	}
	var result struct {
		Text    string `json:"text"`
		Message string `json:"message"`
	}
	_ = json.Unmarshal(request.RunResult, &result)
	reply := strings.TrimSpace(result.Text)
	switch state {
	case "completed":
		view["reply"] = truncateAgentRuntimeText(reply, agentReplyLimit)
	case "failed", "canceled":
		reason := strings.TrimSpace(request.RunErrorMessage)
		if reason == "" {
			reason = strings.TrimSpace(result.Message)
		}
		if reason == "" {
			reason = "The agent stopped without a reply."
		}
		view["error"] = truncateAgentRuntimeText(reason, 2_000)
	case "awaiting_approval":
		view["note"] = "Waiting for " + request.TargetOwnerName + " to approve. Nothing runs until they do; the request expires after 24 hours. Call agents_request_status later, or carry on without it."
	default:
		view["note"] = "Still working. Call agents_request_status with this request_id to keep waiting."
	}
	return TestingMustAPIRawJSON(view)
}
