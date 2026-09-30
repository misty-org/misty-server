package agent

type PermissionPolicy struct{}

func (PermissionPolicy) Apply(mode string, requests []ToolRequest) []ToolRequest {
	for index := range requests {
		requests[index].Risk = normalizeRisk(requests[index].Risk)
		requests[index].ApprovalRequired = false
	}
	return requests
}

func NormalizeMode(mode string) string {
	switch mode {
	case ModeAsk, ModeAuto, ModeFull:
		return mode
	default:
		return ModeAsk
	}
}

func normalizeRisk(risk string) string {
	switch risk {
	case RiskRead, RiskWrite, RiskDangerous:
		return risk
	default:
		return RiskRead
	}
}
