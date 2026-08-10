package billing

import (
	"encoding/json"
	"errors"
	"math"
	"strconv"
	"strings"
	"time"

	envconfig "github.com/kannachi323/misty/server/internal/platform/config"

	agent "github.com/kannachi323/misty/server/internal/agents"
	db "github.com/kannachi323/misty/server/internal/platform/postgres"
)

type CreditMeter struct {
	database *db.Database
	now      func() time.Time
}

func NewCreditMeter(database *db.Database) *CreditMeter {
	return &CreditMeter{database: database, now: time.Now}
}

type modelRates struct {
	input, cachedInput, output int64 // thousandths of USD per one million tokens
}

type configuredModelRates struct {
	Input       int64 `json:"input"`
	CachedInput int64 `json:"cached_input"`
	Output      int64 `json:"output"`
}

const (
	creditCostBufferPercent int64 = 25
)

func ratesForModel(model string) modelRates {
	model = strings.ToLower(strings.TrimSpace(model))
	var configured map[string]configuredModelRates
	if json.Unmarshal([]byte(strings.TrimSpace(envconfig.Getenv("MISTY_HOSTED_AI_MODEL_RATES_JSON"))), &configured) == nil {
		if rates, ok := configured[model]; ok && rates.Input >= 0 && rates.CachedInput >= 0 && rates.Output >= 0 {
			return modelRates{input: rates.Input, cachedInput: rates.CachedInput, output: rates.Output}
		}
	}
	if input, cachedInput, output, ok := agent.CachedGatewayModelRates(model); ok {
		return modelRates{input: input, cachedInput: cachedInput, output: output}
	}
	switch {
	case strings.Contains(model, "gemini-3.5-flash"):
		return modelRates{input: 1500, cachedInput: 150, output: 9000}
	case strings.Contains(model, "gemini-3.1-pro"):
		return modelRates{input: 2000, cachedInput: 200, output: 12000}
	case strings.Contains(model, "gemini-2.5-pro"):
		return modelRates{input: 1250, cachedInput: 125, output: 10000}
	case strings.Contains(model, "gemini-2.5-flash-lite"):
		return modelRates{input: 100, cachedInput: 10, output: 400}
	case strings.Contains(model, "gemini-2.5-flash"):
		return modelRates{input: 300, cachedInput: 30, output: 2500}
	case strings.Contains(model, "5.6-sol"):
		return modelRates{input: 5000, cachedInput: 500, output: 30000}
	case strings.Contains(model, "5.6-terra"):
		return modelRates{input: 2500, cachedInput: 250, output: 15000}
	case strings.Contains(model, "5.6-luna"):
		return modelRates{input: 1000, cachedInput: 100, output: 6000}
	case strings.Contains(model, "5.5-pro"), strings.Contains(model, "5.4-pro"):
		return modelRates{input: 30000, cachedInput: 30000, output: 180000}
	case strings.Contains(model, "5.5"):
		return modelRates{input: 5000, cachedInput: 500, output: 30000}
	case strings.Contains(model, "5.4-mini"):
		return modelRates{input: 750, cachedInput: 75, output: 4500}
	case strings.Contains(model, "5.4-nano"):
		return modelRates{input: 200, cachedInput: 20, output: 1250}
	case strings.Contains(model, "5.4"):
		return modelRates{input: 2500, cachedInput: 250, output: 15000}
	case strings.Contains(model, "gpt-5"):
		return modelRates{input: 1250, cachedInput: 125, output: 10000}
	default:
		// Conservative fallback matching the current flagship rate class.
		return modelRates{input: 5000, cachedInput: 500, output: 30000}
	}
}

func TestingCreditsForUsage(model string, usage agent.ModelUsage) int64 {
	return bufferedHostedAICharge(providerCostForUsage(model, usage))
}

func providerCostForUsage(model string, usage agent.ModelUsage) int64 {
	rates := ratesForModel(model)
	noncachedInput := usage.InputTokens - usage.CachedInputTokens
	if noncachedInput < 0 {
		noncachedInput = 0
	}
	numerator := int64(0)
	numerator = saturatingTokenCost(numerator, noncachedInput, rates.input)
	numerator = saturatingTokenCost(numerator, usage.CachedInputTokens, rates.cachedInput)
	numerator = saturatingTokenCost(numerator, usage.OutputTokens, rates.output)

	// One internal unit represents one millionth of a dollar of weighted provider cost.
	// Rates are stored as thousandths of USD per million tokens, so dividing the
	// weighted numerator by 1,000 converts it to micro-USD. The 25% buffer
	// covers provider price drift and operating overhead without exposing the
	// concrete provider or model to customers.
	denominator := db.CreditDenominationScale
	if numerator > math.MaxInt64-denominator+1 {
		return math.MaxInt64
	}
	cost := (numerator + denominator - 1) / denominator
	if cost < 1 {
		return 1
	}
	return cost
}

func bufferedHostedAICharge(providerCost int64) int64 {
	if providerCost < 1 {
		return 1
	}
	if providerCost > math.MaxInt64/(100+creditCostBufferPercent) {
		return math.MaxInt64
	}
	return (providerCost*(100+creditCostBufferPercent) + 99) / 100
}

func ProviderCostFromBufferedCharge(charge int64) int64 {
	if charge < 1 {
		return 1
	}
	return max(int64(1), (charge*100)/(100+creditCostBufferPercent))
}

func HostedAIChargeForUsage(model string, usage agent.ModelUsage) int64 {
	return TestingCreditsForUsage(model, usage)
}

func ProviderCostForUsage(model string, usage agent.ModelUsage) int64 {
	return providerCostForUsage(model, usage)
}

func EstimateSmartLibraryCharge(imageCount int) int64 {
	if imageCount < 1 {
		return 1
	}
	// Covers the normal vision pass, multimodal embedding, and a full fallback.
	return int64(imageCount) * 1_500
}

func SmartLibraryCharge(usage agent.ModelUsage, embeddedImages int) int64 {
	charge := HostedAIChargeForUsage(agent.SmartLibraryPrimaryModel, usage)
	if embeddedImages > 0 {
		// The default Gemini Embedding 2 list price is $0.00012 per image.
		providerCost := saturatingTokenCost(0, int64(embeddedImages), configurableMicrousd("MISTY_HOSTED_AI_EMBEDDING_IMAGE_MICROUSD", 120))
		charge += bufferedHostedAICharge(providerCost)
	}
	return max(int64(1), charge)
}

func SmartLibraryProviderCost(usage agent.ModelUsage, embeddedImages int) int64 {
	cost := providerCostForUsage(agent.SmartLibraryPrimaryModel, usage)
	if embeddedImages > 0 {
		cost = saturatingTokenCost(cost, int64(embeddedImages), configurableMicrousd("MISTY_HOSTED_AI_EMBEDDING_IMAGE_MICROUSD", 120))
	}
	return cost
}

func EstimateSemanticQueryCharge() int64 { return 250 }

func SemanticQueryCharge(usage agent.ModelUsage) int64 {
	return bufferedHostedAICharge(SemanticQueryProviderCost(usage))
}

func SemanticQueryProviderCost(usage agent.ModelUsage) int64 {
	rate := configurableMicrousd("MISTY_HOSTED_AI_EMBEDDING_TOKEN_MILLIUSD", 150)
	weighted := saturatingTokenCost(0, usage.InputTokens, rate)
	if weighted > math.MaxInt64-db.CreditDenominationScale+1 {
		return math.MaxInt64
	}
	return max(int64(1), (weighted+db.CreditDenominationScale-1)/db.CreditDenominationScale)
}

func EstimateMediaIndexCharge(durationMS int64) int64 {
	if durationMS < 1 {
		return 1
	}
	// A conservative $0.015/minute reservation covers STT, eight visual frames,
	// embeddings, and occasional fallback without turning minutes into a product quota.
	return max(int64(1), (durationMS*15_000+59_999)/60_000)
}

func MediaIndexCharge(durationMS int64, usage agent.ModelUsage) int64 {
	// The default batch transcription rate is $0.10/hour. Vision and text embeddings are token-metered.
	hourlyRate := configurableMicrousd("MISTY_HOSTED_AI_TRANSCRIPTION_HOUR_MICROUSD", 100_000)
	weightedDuration := saturatingTokenCost(0, durationMS, hourlyRate)
	sttProviderMicrousd := int64(math.MaxInt64)
	if weightedDuration <= math.MaxInt64-3_599_999 {
		sttProviderMicrousd = max(int64(1), (weightedDuration+3_599_999)/3_600_000)
	}
	sttCharge := bufferedHostedAICharge(sttProviderMicrousd)
	return max(int64(1), sttCharge+HostedAIChargeForUsage(agent.SmartLibraryPrimaryModel, usage))
}

func MediaIndexProviderCost(durationMS int64, usage agent.ModelUsage) int64 {
	hourlyRate := configurableMicrousd("MISTY_HOSTED_AI_TRANSCRIPTION_HOUR_MICROUSD", 100_000)
	weightedDuration := saturatingTokenCost(0, durationMS, hourlyRate)
	sttCost := int64(math.MaxInt64)
	if weightedDuration <= math.MaxInt64-3_599_999 {
		sttCost = max(int64(1), (weightedDuration+3_599_999)/3_600_000)
	}
	modelCost := providerCostForUsage(agent.SmartLibraryPrimaryModel, usage)
	if sttCost > math.MaxInt64-modelCost {
		return math.MaxInt64
	}
	return sttCost + modelCost
}

func configurableMicrousd(name string, fallback int64) int64 {
	value, err := strconv.ParseInt(strings.TrimSpace(envconfig.Getenv(name)), 10, 64)
	if err != nil || value < 1 {
		return fallback
	}
	return value
}

func saturatingTokenCost(total, tokens, rate int64) int64 {
	if tokens <= 0 || rate <= 0 {
		return total
	}
	if tokens > (math.MaxInt64-total)/rate {
		return math.MaxInt64
	}
	return total + tokens*rate
}

func (meter *CreditMeter) Reserve(userID, idempotencyKey, usageMeter, provider, model string, estimatedInputTokens, maxOutputTokens int64) (*agent.UsageReservation, error) {
	license, err := meter.database.GetLicenseByUserID(userID)
	if err != nil {
		return nil, err
	}
	tier := db.TierBasic
	if license != nil {
		tier = license.Tier
	}
	estimate := agent.ModelUsage{InputTokens: estimatedInputTokens, OutputTokens: maxOutputTokens, Estimated: true}
	credits := TestingCreditsForUsage(model, estimate)
	// A completion's estimate assumes the maximum possible output. If less than
	// that remains, reserve the whole remainder and settle against actual usage
	// instead of claiming the weekly allowance is already exhausted.
	reservation, wallet, err := meter.database.ReserveHostedAIUsageUpTo(userID, tier, usageMeter, idempotencyKey, credits, meter.now())
	if err != nil {
		var insufficient db.HostedAILimitReachedError
		if errors.As(err, &insufficient) {
			resetAt := time.Time{}
			if wallet != nil {
				resetAt = wallet.ResetAt
			}
			return nil, agent.HostedAILimitReachedError{Required: insufficient.Required, Available: insufficient.Available, ResetAt: resetAt}
		}
		return nil, err
	}
	return &agent.UsageReservation{ID: reservation.ID, ReservedMicrousd: reservation.ReservedMicrousd, ReservedCredits: reservation.ReservedMicrousd}, nil
}

func (meter *CreditMeter) Settle(reservation *agent.UsageReservation, idempotencyKey, usageMeter, provider, model string, usage agent.ModelUsage) (agent.UsageSettlement, error) {
	if usage.InputTokens == 0 && usage.OutputTokens == 0 {
		// Some providers do not expose usage. Charge the conservative reservation rather than allowing unmetered managed AI.
		usage.Estimated = true
		usage.OutputTokens = 1
	}
	charge := TestingCreditsForUsage(model, usage)
	reserved := reservation.ReservedMicrousd
	if reserved == 0 {
		reserved = reservation.ReservedCredits
	}
	if usage.Estimated && charge < reserved {
		charge = reserved
	}
	wallet, err := meter.database.SettleCreditReservation(reservation.ID, idempotencyKey, db.CreditUsage{
		Provider: provider, Model: model, InputTokens: usage.InputTokens,
		CachedInputTokens: usage.CachedInputTokens, OutputTokens: usage.OutputTokens,
		ReasoningTokens: usage.ReasoningTokens, ProviderCost: providerCostForUsage(model, usage), ChargeMicrousd: charge,
	})
	if err != nil {
		return agent.UsageSettlement{}, err
	}
	return agent.UsageSettlement{ChargedMicrousd: charge, UsedRatio: wallet.UsedRatio(), ResetAt: wallet.ResetAt, CreditsUsed: charge, CreditsRemaining: wallet.Available()}, nil
}

func (meter *CreditMeter) Release(reservation *agent.UsageReservation) error {
	if reservation == nil {
		return nil
	}
	return meter.database.ReleaseCreditReservation(reservation.ID)
}

// Refund returns a settled model call to the member's weekly allowance when
// Misty itself rejected the model's response. The provider cost remains in the
// audit ledger, but a capability-envelope mismatch is our failure rather than
// useful Agent work and must not consume the member's quota.
func (meter *CreditMeter) Refund(reservation *agent.UsageReservation, idempotencyKey, reason string) (agent.UsageSettlement, error) {
	if reservation == nil {
		return agent.UsageSettlement{}, nil
	}
	wallet, err := meter.database.RefundHostedAIReservation(reservation.ID, idempotencyKey, reason)
	if err != nil {
		return agent.UsageSettlement{}, err
	}
	return agent.UsageSettlement{
		UsedRatio: wallet.UsedRatio(), ResetAt: wallet.ResetAt,
		CreditsRemaining: wallet.Available(),
	}, nil
}
