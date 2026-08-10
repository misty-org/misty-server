package agent

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	. "github.com/kannachi323/misty/server/internal/agents"
)

// countingProvider records how many calls actually reached the model, which is
// what a provider would bill for.
type countingProvider struct {
	mu    sync.Mutex
	calls int
	block chan struct{}
}

func (p *countingProvider) Next(ModelRequest) (ModelResponse, error) {
	p.mu.Lock()
	p.calls++
	p.mu.Unlock()
	if p.block != nil {
		<-p.block
	}
	return ModelResponse{}, nil
}

func (p *countingProvider) Count() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.calls
}

func TestProviderBudgetCapsCallsPerMinute(t *testing.T) {
	inner := &countingProvider{}
	provider := NewBudgetedProvider(inner, ProviderBudget{
		PerMinute: 5, PerHour: 1000, PerDay: 1000, MaxConcurrent: 8,
	})

	refused := 0
	for i := 0; i < 50; i++ {
		if _, err := provider.Next(ModelRequest{}); errors.Is(err, ErrProviderBudgetExceeded) {
			refused++
		}
	}
	if inner.Count() != 5 {
		t.Fatalf("provider was billed for %d calls, want the 5/min ceiling", inner.Count())
	}
	if refused != 45 {
		t.Fatalf("refused %d calls, want 45", refused)
	}
}

func TestProviderBudgetCapsDailySpendEvenAsMinutesPass(t *testing.T) {
	inner := &countingProvider{}
	provider := NewBudgetedProvider(inner, ProviderBudget{
		PerMinute: 100, PerHour: 100, PerDay: 7, MaxConcurrent: 8,
	})
	current := time.Now()
	provider.TestingNow = func() time.Time { return current }

	// Walking the clock forward clears the minute and hour windows, but the
	// daily ceiling is what actually bounds a day's bill.
	for i := 0; i < 50; i++ {
		_, _ = provider.Next(ModelRequest{})
		current = current.Add(2 * time.Minute)
	}
	if inner.Count() != 7 {
		t.Fatalf("provider was billed for %d calls, want the 7/day ceiling", inner.Count())
	}
}

func TestProviderBudgetRefusalDoesNotConsumeOtherWindows(t *testing.T) {
	inner := &countingProvider{}
	provider := NewBudgetedProvider(inner, ProviderBudget{
		PerMinute: 10, PerHour: 10, PerDay: 2, MaxConcurrent: 8,
	})
	for i := 0; i < 5; i++ {
		_, _ = provider.Next(ModelRequest{})
	}
	// Two calls landed; the day limit refused the rest. Those refusals must not
	// have burned minute budget, or a blocked day would also wedge the minute.
	provider.TestingMu.Lock()
	minuteUsed := len(provider.TestingMinute.TestingEvents)
	provider.TestingMu.Unlock()
	if minuteUsed != 2 {
		t.Fatalf("minute window charged %d calls, want only the 2 that ran", minuteUsed)
	}
}

func TestProviderBudgetCapsConcurrency(t *testing.T) {
	inner := &countingProvider{block: make(chan struct{})}
	provider := NewBudgetedProvider(inner, ProviderBudget{
		PerMinute: 100, PerHour: 100, PerDay: 100, MaxConcurrent: 2,
	})

	var wait sync.WaitGroup
	for i := 0; i < 2; i++ {
		wait.Add(1)
		go func() {
			defer wait.Done()
			_, _ = provider.Next(ModelRequest{})
		}()
	}
	// Wait for both slots to be occupied.
	deadline := time.Now().Add(2 * time.Second)
	for inner.Count() < 2 && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}

	if _, err := provider.Next(ModelRequest{}); !errors.Is(err, ErrProviderBudgetExceeded) {
		t.Fatalf("third concurrent call error = %v, want the ceiling to refuse it", err)
	}
	close(inner.block)
	wait.Wait()
}

func TestProviderBudgetReleasesSlotsAfterCompletion(t *testing.T) {
	inner := &countingProvider{}
	provider := NewBudgetedProvider(inner, ProviderBudget{
		PerMinute: 100, PerHour: 100, PerDay: 100, MaxConcurrent: 1,
	})
	for i := 0; i < 5; i++ {
		if _, err := provider.Next(ModelRequest{}); err != nil {
			t.Fatalf("sequential call %d was refused: %v", i, err)
		}
	}
}

func TestProviderBudgetFromEnvRejectsNonsense(t *testing.T) {
	t.Setenv("MISTY_AI_MAX_CALLS_PER_MINUTE", "0")
	t.Setenv("MISTY_AI_MAX_CALLS_PER_HOUR", "not-a-number")
	t.Setenv("MISTY_AI_MAX_CALLS_PER_DAY", "-5")
	budget := ProviderBudgetFromEnv()
	// A misconfigured ceiling must fall back to the safe default rather than
	// becoming unlimited.
	if budget.PerMinute != DefaultProviderBudget().PerMinute ||
		budget.PerHour != DefaultProviderBudget().PerHour ||
		budget.PerDay != DefaultProviderBudget().PerDay {
		t.Fatalf("ProviderBudgetFromEnv() = %+v, want the safe defaults", budget)
	}
}

func TestProviderBudgetHonoursConfiguredCeiling(t *testing.T) {
	t.Setenv("MISTY_AI_MAX_CALLS_PER_MINUTE", "3")
	if got := ProviderBudgetFromEnv().PerMinute; got != 3 {
		t.Fatalf("PerMinute = %d, want the configured 3", got)
	}
}

func TestBudgetedProviderPassesContextThrough(t *testing.T) {
	provider := NewBudgetedProvider(&countingProvider{}, DefaultProviderBudget())
	if _, err := provider.NextContext(context.Background(), ModelRequest{}); err != nil {
		t.Fatalf("NextContext() error = %v", err)
	}
}

// tokenProvider reports a fixed billable usage per call.
type tokenProvider struct {
	mu     sync.Mutex
	calls  int
	tokens int64
}

func (p *tokenProvider) Next(ModelRequest) (ModelResponse, error) {
	p.mu.Lock()
	p.calls++
	p.mu.Unlock()
	return ModelResponse{Usage: ModelUsage{InputTokens: p.tokens}}, nil
}

func (p *tokenProvider) Count() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.calls
}

func TestProviderBudgetCapsBillableTokensNotJustCalls(t *testing.T) {
	// Ten calls are well inside the call ceiling, but each is expensive. A
	// call-count limit alone would let all of them through.
	inner := &tokenProvider{tokens: 100_000}
	provider := NewBudgetedProvider(inner, ProviderBudget{
		PerMinute: 1000, PerHour: 1000, PerDay: 1000, MaxConcurrent: 8,
		TokensPerHour: 250_000, TokensPerDay: 250_000,
	})

	refused := 0
	for i := 0; i < 10; i++ {
		if _, err := provider.Next(ModelRequest{}); errors.Is(err, ErrProviderTokenBudgetExceeded) {
			refused++
		}
	}
	// Three calls fit before the running total crosses the ceiling.
	if inner.Count() != 3 {
		t.Fatalf("provider was billed for %d calls, want 3 before the token ceiling", inner.Count())
	}
	if refused != 7 {
		t.Fatalf("refused %d calls, want 7", refused)
	}
	if provider.SpentTokens() != 300_000 {
		t.Fatalf("SpentTokens() = %d, want 300000", provider.SpentTokens())
	}
}

func TestProviderTokenBudgetRecoversAsTheWindowRolls(t *testing.T) {
	inner := &tokenProvider{tokens: 100_000}
	provider := NewBudgetedProvider(inner, ProviderBudget{
		PerMinute: 1000, PerHour: 1000, PerDay: 1000, MaxConcurrent: 8,
		TokensPerHour: 150_000, TokensPerDay: 10_000_000,
	})
	current := time.Now()
	provider.TestingNow = func() time.Time { return current }

	_, _ = provider.Next(ModelRequest{})
	_, _ = provider.Next(ModelRequest{})
	if _, err := provider.Next(ModelRequest{}); !errors.Is(err, ErrProviderTokenBudgetExceeded) {
		t.Fatalf("third call error = %v, want the token ceiling to refuse it", err)
	}

	// An hour later the spend has aged out and work resumes.
	current = current.Add(61 * time.Minute)
	if _, err := provider.Next(ModelRequest{}); err != nil {
		t.Fatalf("call after the window rolled was refused: %v", err)
	}
}

func TestProviderTokenBudgetCountsOutputAndReasoning(t *testing.T) {
	provider := NewBudgetedProvider(&countingProvider{}, DefaultProviderBudget())
	provider.TestingRecordSpend(ModelResponse{Usage: ModelUsage{
		InputTokens: 10, OutputTokens: 20, ReasoningTokens: 30,
	}})
	// Output and reasoning tokens are billed too, often at a higher rate.
	if provider.SpentTokens() != 60 {
		t.Fatalf("SpentTokens() = %d, want 60", provider.SpentTokens())
	}
}

func TestProviderTokenBudgetFromEnv(t *testing.T) {
	t.Setenv("MISTY_AI_MAX_TOKENS_PER_DAY", "1234")
	t.Setenv("MISTY_AI_MAX_TOKENS_PER_HOUR", "bogus")
	budget := ProviderBudgetFromEnv()
	if budget.TokensPerDay != 1234 {
		t.Fatalf("TokensPerDay = %d, want the configured 1234", budget.TokensPerDay)
	}
	if budget.TokensPerHour != DefaultProviderBudget().TokensPerHour {
		t.Fatalf("TokensPerHour = %d, want the safe default", budget.TokensPerHour)
	}
}

func TestUnsetBudgetFieldsFallBackToSafeDefaultsNotZero(t *testing.T) {
	// A zero limit must never mean "refuse everything" (which would wedge the
	// deployment) nor "unlimited" (which would defeat the point).
	provider := NewBudgetedProvider(&countingProvider{}, ProviderBudget{MaxConcurrent: 4})
	if _, err := provider.Next(ModelRequest{}); err != nil {
		t.Fatalf("a budget with unset windows refused a call: %v", err)
	}
	defaults := DefaultProviderBudget()
	if provider.TestingMinute.TestingLimit != defaults.PerMinute || provider.TestingTokenDay.TestingLimit != defaults.TokensPerDay {
		t.Fatalf("unset fields = %d/%d, want the safe defaults %d/%d",
			provider.TestingMinute.TestingLimit, provider.TestingTokenDay.TestingLimit, defaults.PerMinute, defaults.TokensPerDay)
	}
}
