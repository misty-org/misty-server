package api

import (
	"encoding/json"
	"strings"

	"github.com/kannachi323/misty/server/internal/platform/db"
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

// memberRequestState is the request's place in A2A's task lifecycle.
func memberRequestState(request *db.AgentMemberRequest) string {
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
	default:
		view["note"] = "Still working. Call agent_request_status with this request_id to keep waiting."
	}
	return MustAPIRawJSON(view)
}
