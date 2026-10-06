package agent

import (
	"strings"


)

func isLiveGatewayRateLimit(err error) bool {
	message := strings.ToLower(err.Error())
	return strings.Contains(message, "status 429") || strings.Contains(message, "rate_limit_exceeded")
}
