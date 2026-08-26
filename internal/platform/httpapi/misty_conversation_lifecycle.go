package api

func mistyTurnMessageState(invocationState, agentState string) string {
	if invocationState == "failed" || agentState == "failed" || agentState == "completed_with_errors" {
		return "failed"
	}
	if invocationState == "canceled" || agentState == "canceled" {
		return "canceled"
	}
	if invocationState == "queued" || invocationState == "running" || agentState == "queued" || agentState == "running" || agentState == "awaiting_approval" {
		return "pending"
	}
	return "completed"
}

func mistyRunActionState(state string) string {
	switch state {
	case "completed", "completed_with_errors":
		return "completed"
	case "failed", "canceled":
		return "failed"
	case "awaiting_approval":
		return "awaiting_approval"
	default:
		return "running"
	}
}

func TestingMistyRunActionState(state string) string {
	return mistyRunActionState(state)
}
