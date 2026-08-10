package agent

import (
	"errors"
	"strconv"
	"strings"
	"sync"
	"time"

	envconfig "github.com/kannachi323/misty/server/internal/platform/config"
)

// A process-wide ceiling on paid model calls.
//
// Per-user credits bound what any one account can spend, and the API rate
// limiter bounds request volume. Neither bounds the *bill*: a bug, a runaway
// tool loop, or many accounts at once can still multiply into real money. This
// wrapper sits at the one place every model call passes through, so no code
// path — chat, media search, library indexing, Agent runs, workflows — can
// exceed it, including paths added later that forget to ask permission.
//
// It is a circuit breaker, not a scheduler: refused calls fail fast and are
// never queued or retried.

// ErrProviderBudgetExceeded is returned when the global call ceiling is reached.
var ErrProviderBudgetExceeded = errors.New("ai provider budget exceeded; try again later")

// ErrProviderTokenBudgetExceeded is returned when the billable token ceiling is
// reached. Separated from the call ceiling so operators can tell a burst of
// small requests from genuinely expensive spend.
var ErrProviderTokenBudgetExceeded = errors.New("ai provider token budget exceeded; try again later")

// ProviderBudget bounds calls per window and concurrency.
type ProviderBudget struct {
	PerMinute     int
	PerHour       int
	PerDay        int
	MaxConcurrent int
	// TokensPerHour/TokensPerDay bound the actual billable quantity. Call
	// counts alone do not: one request with a huge context can cost more than
	// a thousand small ones, so a call ceiling is a poor proxy for a bill.
	TokensPerHour int64
	TokensPerDay  int64
}

// DefaultProviderBudget is intentionally conservative. It is a ceiling for the
// whole deployment, not a per-user allowance, and is meant to be raised
// deliberately rather than discovered through a bill.
func DefaultProviderBudget() ProviderBudget {
	return ProviderBudget{
		PerMinute: 90, PerHour: 1500, PerDay: 10000, MaxConcurrent: 8,
		TokensPerHour: 2_000_000, TokensPerDay: 20_000_000,
	}
}

// ProviderBudgetFromEnv reads the ceiling, falling back to the safe default.
func ProviderBudgetFromEnv() ProviderBudget {
	budget := DefaultProviderBudget()
	budget.PerMinute = positiveEnvInt("MISTY_AI_MAX_CALLS_PER_MINUTE", budget.PerMinute)
	budget.PerHour = positiveEnvInt("MISTY_AI_MAX_CALLS_PER_HOUR", budget.PerHour)
	budget.PerDay = positiveEnvInt("MISTY_AI_MAX_CALLS_PER_DAY", budget.PerDay)
	budget.MaxConcurrent = positiveEnvInt("MISTY_AI_MAX_CONCURRENT", budget.MaxConcurrent)
	budget.TokensPerHour = positiveEnvInt64("MISTY_AI_MAX_TOKENS_PER_HOUR", budget.TokensPerHour)
	budget.TokensPerDay = positiveEnvInt64("MISTY_AI_MAX_TOKENS_PER_DAY", budget.TokensPerDay)
	return budget
}

func positiveEnvInt64(name string, fallback int64) int64 {
	value, err := strconv.ParseInt(strings.TrimSpace(envconfig.Getenv(name)), 10, 64)
	if err != nil || value <= 0 {
		return fallback
	}
	return value
}

func positiveEnvInt(name string, fallback int) int {
	value, err := strconv.Atoi(strings.TrimSpace(envconfig.Getenv(name)))
	if err != nil || value <= 0 {
		return fallback
	}
	return value
}

type budgetWindow struct {
	TestingLimit  int
	window        time.Duration
	TestingEvents []time.Time
}

// hasRoom prunes elapsed events and reports whether another call fits. It
// deliberately does not charge: a call refused by a later window must not
// consume budget in an earlier one.
func (w *budgetWindow) hasRoom(now time.Time) bool {
	cutoff := now.Add(-w.window)
	live := w.TestingEvents[:0]
	for _, event := range w.TestingEvents {
		if event.After(cutoff) {
			live = append(live, event)
		}
	}
	w.TestingEvents = live
	return len(w.TestingEvents) < w.TestingLimit
}

func (w *budgetWindow) record(now time.Time) {
	w.TestingEvents = append(w.TestingEvents, now)
}

// BudgetedProvider wraps a ModelProvider with the global ceiling.
type BudgetedProvider struct {
	inner ModelProvider

	TestingMu       sync.Mutex
	TestingNow      func() time.Time
	TestingMinute   *budgetWindow
	hour            *budgetWindow
	day             *budgetWindow
	inFlight        int
	maxFlight       int
	refusals        int64
	lastRefusal     time.Time
	tokenHour       *tokenWindow
	TestingTokenDay *tokenWindow
	spentTokens     int64
}

// tokenWindow tracks billable tokens spent inside a rolling window. Tokens are
// only known after a call returns, so the ceiling is enforced on the running
// total: one request may overshoot, but the next is refused.
type tokenWindow struct {
	TestingLimit int64
	window       time.Duration
	events       []tokenEvent
	total        int64
}

type tokenEvent struct {
	at     time.Time
	tokens int64
}

func (w *tokenWindow) prune(now time.Time) {
	cutoff := now.Add(-w.window)
	live := w.events[:0]
	total := int64(0)
	for _, event := range w.events {
		if event.at.After(cutoff) {
			live = append(live, event)
			total += event.tokens
		}
	}
	w.events, w.total = live, total
}

func (w *tokenWindow) hasRoom(now time.Time) bool {
	w.prune(now)
	return w.total < w.TestingLimit
}

func (w *tokenWindow) record(now time.Time, tokens int64) {
	if tokens <= 0 {
		return
	}
	w.events = append(w.events, tokenEvent{at: now, tokens: tokens})
	w.total += tokens
}

// NewBudgetedProvider returns provider wrapped in the given ceiling.
func NewBudgetedProvider(provider ModelProvider, budget ProviderBudget) *BudgetedProvider {
	if budget.MaxConcurrent <= 0 {
		budget = DefaultProviderBudget()
	}
	// An unset ceiling must mean "use the safe default", never "unlimited" and
	// never "refuse everything". A zero limit would otherwise wedge the whole
	// deployment the moment a caller built a budget without these fields.
	defaults := DefaultProviderBudget()
	if budget.PerMinute <= 0 {
		budget.PerMinute = defaults.PerMinute
	}
	if budget.PerHour <= 0 {
		budget.PerHour = defaults.PerHour
	}
	if budget.PerDay <= 0 {
		budget.PerDay = defaults.PerDay
	}
	if budget.TokensPerHour <= 0 {
		budget.TokensPerHour = defaults.TokensPerHour
	}
	if budget.TokensPerDay <= 0 {
		budget.TokensPerDay = defaults.TokensPerDay
	}
	return &BudgetedProvider{
		inner:           provider,
		TestingNow:      time.Now,
		TestingMinute:   &budgetWindow{TestingLimit: budget.PerMinute, window: time.Minute},
		hour:            &budgetWindow{TestingLimit: budget.PerHour, window: time.Hour},
		day:             &budgetWindow{TestingLimit: budget.PerDay, window: 24 * time.Hour},
		maxFlight:       budget.MaxConcurrent,
		tokenHour:       &tokenWindow{TestingLimit: budget.TokensPerHour, window: time.Hour},
		TestingTokenDay: &tokenWindow{TestingLimit: budget.TokensPerDay, window: 24 * time.Hour},
	}
}

// acquire charges one call against every window, or refuses.
func (p *BudgetedProvider) acquire() (func(), error) {
	p.TestingMu.Lock()
	defer p.TestingMu.Unlock()
	now := p.TestingNow()

	if p.inFlight >= p.maxFlight {
		p.refuse(now)
		return nil, ErrProviderBudgetExceeded
	}
	// Every window is checked before any is charged, so a call refused by the
	// day limit does not also consume minute budget.
	if !p.TestingMinute.hasRoom(now) || !p.hour.hasRoom(now) || !p.day.hasRoom(now) {
		p.refuse(now)
		return nil, ErrProviderBudgetExceeded
	}
	if !p.tokenHour.hasRoom(now) || !p.TestingTokenDay.hasRoom(now) {
		p.refuse(now)
		return nil, ErrProviderTokenBudgetExceeded
	}
	p.TestingMinute.record(now)
	p.hour.record(now)
	p.day.record(now)
	p.inFlight++
	return func() {
		p.TestingMu.Lock()
		if p.inFlight > 0 {
			p.inFlight--
		}
		p.TestingMu.Unlock()
	}, nil
}

func (p *BudgetedProvider) refuse(now time.Time) {
	p.refusals++
	p.lastRefusal = now
}

// Refusals reports how many calls the ceiling has rejected, for monitoring.
func (p *BudgetedProvider) Refusals() int64 {
	p.TestingMu.Lock()
	defer p.TestingMu.Unlock()
	return p.refusals
}
