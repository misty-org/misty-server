package api

import (
	"context"
	"errors"
	"log"
	"net/http"
	"regexp"
	"strings"
	"sync"
	"time"

	db "github.com/kannachi323/misty/server/internal/platform/postgres"
)

// Coding (projects, coding runs, project grants and GitHub for projects) is a
// Pro-and-above feature. Every coding entry point asks codingAccess, whatever
// calls it: the app, an agent run, the MCP server, an A2A caller or a script.
// The plan is the real control; the outside-agent check is defense in depth.

const (
	codePlanUpgradeRequired = "plan_upgrade_required"
	codeOutsideAgentBlocked = "outside_agent_blocked"

	planUpgradeMessage  = "Coding is part of Misty Pro and above. Upgrade your plan to use projects and coding tools."
	outsideAgentMessage = "Coding works only from the Misty app. This request came from another program, so Misty stopped it."
)

var errPlanUpgradeRequired = errors.New(codePlanUpgradeRequired)

// codingPlans caches each account's answer briefly: the toolbox asks on every
// tool call, and billing should not be asked each time.
var codingPlans = struct {
	sync.Mutex
	entries map[string]codingPlanEntry
}{entries: map[string]codingPlanEntry{}}

type codingPlanEntry struct {
	included bool
	expires  time.Time
}

// codingIncluded is nil when the account's plan includes coding,
// errPlanUpgradeRequired when it does not, and the billing error when billing
// can't answer: coding stays closed then.
func codingIncluded(ctx context.Context, database *db.Database, user string) error {
	codingPlans.Lock()
	entry, ok := codingPlans.entries[user]
	codingPlans.Unlock()
	if !ok || time.Now().After(entry.expires) {
		entitlements, err := database.EntitlementsForUser(ctx, user)
		if err != nil {
			return err
		}
		entry = codingPlanEntry{included: entitlements.Coding, expires: time.Now().Add(30 * time.Second)}
		codingPlans.Lock()
		if len(codingPlans.entries) > 10_000 {
			codingPlans.entries = map[string]codingPlanEntry{}
		}
		codingPlans.entries[user] = entry
		codingPlans.Unlock()
	}
	if !entry.included {
		return errPlanUpgradeRequired
	}
	return nil
}

// desktopUserAgentPrefix is how the Misty app's account client names itself.
const desktopUserAgentPrefix = "MistyDesktop/"

// automationAgents are user agents of HTTP libraries, command-line tools,
// browser automation and AI agents. The Misty app never sends these.
var automationAgents = regexp.MustCompile(`(?i)curl|wget|httpie|python|aiohttp|httpx|requests|urllib|node-fetch|undici|axios|got/|deno|bun/|go-http-client|okhttp|java/|libwww|postman|insomnia|powershell|playwright|puppeteer|selenium|webdriver|headless|phantomjs|claude|anthropic|openai|chatgpt|gpt|langchain|llamaindex|crewai|autogen|mcp|a2a|agent`)

var automationReason = regexp.MustCompile(`^[a-z_]{1,32}$`)

// outsideAgentReason says why a request looks like an outside program rather
// than a person in the Misty app, or "" when it carries the app's signals.
//
// The app's account client always sends its own user agent and X-Misty-CSRF,
// never an Origin, and tells the server when its window is
// driven by WebDriver. A determined caller can copy every one of these, so
// this catches agents and scripts that don't try to hide; the plan gate holds
// either way.
func outsideAgentReason(r *http.Request) string {
	if reason := strings.TrimSpace(r.Header.Get("X-Misty-Automation")); reason != "" {
		if !automationReason.MatchString(reason) {
			reason = "reported"
		}
		return "app_automation_" + reason
	}
	agent := strings.TrimSpace(r.UserAgent())
	product, _, _ := strings.Cut(agent, " ")
	switch {
	case agent == "":
		return "no_user_agent"
	case automationAgents.MatchString(agent) && !strings.HasPrefix(agent, desktopUserAgentPrefix):
		return "automation_user_agent"
	case !strings.HasPrefix(product, desktopUserAgentPrefix) || automationAgents.MatchString(strings.TrimPrefix(agent, product)):
		return "not_misty_app"
	case r.Header.Get("X-Misty-CSRF") != "1":
		return "missing_app_header"
	case r.Header.Get("Origin") != "":
		return "browser_page"
	}
	return ""
}

// logOutsideAgent records a blocked request. It never logs credentials.
func logOutsideAgent(r *http.Request, user, entry, reason string) {
	log.Printf("coding.outside_agent_blocked entry=%s reason=%s user=%s user_agent=%q", entry, reason, user, truncateAgentRuntimeText(r.UserAgent(), 160))
}

// requireCoding admits a coding request: the account's plan must include
// coding, and the request must come from the Misty app. It writes the refusal
// and returns false otherwise.
func requireCoding(w http.ResponseWriter, r *http.Request, database *db.Database, user, entry string) bool {
	if reason := outsideAgentReason(r); reason != "" {
		logOutsideAgent(r, user, entry, reason)
		writeJSON(w, http.StatusForbidden, map[string]string{"code": codeOutsideAgentBlocked, "message": outsideAgentMessage})
		return false
	}
	switch err := codingIncluded(r.Context(), database, user); {
	case errors.Is(err, errPlanUpgradeRequired):
		writeJSON(w, http.StatusPaymentRequired, map[string]string{"code": codePlanUpgradeRequired, "message": planUpgradeMessage})
		return false
	case err != nil:
		writeBillingError(w, err)
		return false
	}
	return true
}

// TestingResetCodingPlans forgets cached plan answers between tests.
func TestingResetCodingPlans() {
	codingPlans.Lock()
	codingPlans.entries = map[string]codingPlanEntry{}
	codingPlans.Unlock()
}
