package api

import (
	"math"
	"strconv"
	"strings"

	"github.com/kannachi323/misty/server/internal/agent"
	"github.com/kannachi323/misty/server/internal/platform/config"
)

// Work done in a person's own coding tool is already paid for by their
// subscription to that tool, so it counts toward Misty's weekly coding
// allowance at a reduced rate: MISTY_CODING_EXTERNAL_FACTOR, 0.25 unless set.
// The billing service receives the same reduced counts; it must not apply a
// factor of its own.
const defaultCodingExternalFactor = 0.25

// cachedReadWeight is how much a cached input token counts against a fresh
// one, matching the providers' discounted cache price.
const cachedReadWeight = 0.1

func codingExternalFactor() float64 {
	value, err := strconv.ParseFloat(strings.TrimSpace(config.Getenv("MISTY_CODING_EXTERNAL_FACTOR")), 64)
	if err != nil || value <= 0 || value > 1 || math.IsNaN(value) {
		return defaultCodingExternalFactor
	}
	return value
}

// codingUsage is what a tool reported, in its own token classes.
type codingUsage struct {
	Input, Output, CacheWrite, CacheRead int64
}

// weighted is the usage in allowance tokens: fresh input, cache writes and
// output at full weight, cached reads at a tenth, all times the factor.
func (u codingUsage) weighted(factor float64) int64 {
	full := float64(u.Input + u.CacheWrite + u.Output)
	return int64(math.Ceil((full + float64(u.CacheRead)*cachedReadWeight) * factor))
}

// billed is the usage the billing service is told about, each class scaled
// by the factor and rounded up.
func (u codingUsage) billed(factor float64) agent.ModelUsage {
	scale := func(n int64) int64 { return int64(math.Ceil(float64(n) * factor)) }
	return agent.ModelUsage{InputTokens: scale(u.Input + u.CacheWrite), CachedInputTokens: scale(u.CacheRead), OutputTokens: scale(u.Output)}
}

// codingBillingProvider names the provider behind each coding tool, so usage
// is priced against that provider's models.
func codingBillingProvider(engine string) string {
	switch engine {
	case "claude":
		return "anthropic"
	case "codex":
		return "openai"
	case "grok":
		return "xai"
	case "kimi":
		return "moonshotai"
	case "minimax":
		return "minimax"
	case "dsh":
		return "deepseek"
	case "qoder", "qoder-cn":
		return "alibaba"
	case "agy":
		return "google"
	}
	return engine
}
