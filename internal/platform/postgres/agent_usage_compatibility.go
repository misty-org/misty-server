package db

// Persisted operation identifiers remain stable for existing Agent histories.
const (
	CreditMeterAgentAI      = "assistant_ai"
	CreditMeterAutomationAI = "automation_ai"
)

// Retained only to recognize errors in older persisted invocations.
// Admission and settlement now belong to billingadapter.
type HostedAILimitReachedError struct {
	Required, Available int64
	Scope               string
}

func (HostedAILimitReachedError) Error() string { return "hosted AI usage limit reached" }
