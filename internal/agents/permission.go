package agent

type PermissionPolicy struct{}

func (PermissionPolicy) Apply(mode string, requests []ToolRequest) []ToolRequest {
	normalized := NormalizeMode(mode)
	for index := range requests {
		requests[index].Risk = normalizeRisk(requests[index].Risk)
		requests[index].ApprovalRequired = approvalRequired(normalized, requests[index])
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

func approvalRequired(mode string, request ToolRequest) bool {
	switch mode {
	case ModeFull:
		return false
	case ModeAuto:
		if request.Risk == RiskRead {
			return false
		}
		if request.Risk == RiskWrite && lowRiskWriteTool(request.Name) {
			return false
		}
		return true
	default:
		return true
	}
}

func lowRiskWriteTool(name string) bool {
	switch name {
	case ToolValidateFilePlan:
		return true
	case ToolApplyFilePlan:
		return true
	default:
		return false
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
