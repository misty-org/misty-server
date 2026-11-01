package api

import (
	"context"
	"regexp"

	"github.com/kannachi323/misty/server/internal/agenttools"
)

// Search apps are paid web search services the user would have to sign up
// for. Misty's own web search runs on the person's device when they keep a
// search key there (docs/agents/runtime.md), so an agent asks to connect one
// only when the user names it. The server can't tell whether this run's device
// has web search, so the advice below holds either way.
var appsSearchServices = map[string]*regexp.Regexp{
	"exa":          regexp.MustCompile(`(?i)\bexa\b`),
	"tavily":       regexp.MustCompile(`(?i)\btavily\b`),
	"serpapi":      regexp.MustCompile(`(?i)\bserp ?api\b`),
	"serper":       regexp.MustCompile(`(?i)\bserper\b`),
	"perplexityai": regexp.MustCompile(`(?i)\bperplexity\b`),
	"firecrawl":    regexp.MustCompile(`(?i)\bfire ?crawl\b`),
}

type searchAppPolicy struct {
	// offer says whether Misty may offer them unprompted (it does not).
	offer bool
	// request is what the user asked for in this run.
	request string
}

func (s *SpacesService) searchAppPolicy(_ context.Context, invocation agenttools.Invocation) searchAppPolicy {
	return searchAppPolicy{
		offer:   offerSearchAppsByDefault,
		request: invocation.OriginalInput,
	}
}

// named reports whether the user asked for this search app by name.
func (p searchAppPolicy) named(app string) bool {
	pattern, ok := appsSearchServices[app]
	return ok && pattern.MatchString(p.request)
}

// mayConnect reports whether the agent may show a Connect card for app.
func (p searchAppPolicy) mayConnect(app string) bool {
	_, search := appsSearchServices[app]
	return !search || p.offer || p.named(app)
}

// next is what integration_search tells the model about an unconnected app.
func (p searchAppPolicy) next(app string) string {
	connect := "Call integration_connect with app " + app + " before running its tools."
	if _, search := appsSearchServices[app]; !search || p.named(app) {
		return connect
	}
	if p.offer {
		return "This is a paid search app the user would sign up for. " + searchAppInstead + " Offer it only if that cannot find what the task needs; then " + connect
	}
	return "Do not connect this paid search app unless the user names it. " + searchAppInstead
}

// refusal explains a blocked Connect card for a search app.
func (p searchAppPolicy) refusal(name string) string {
	return "The user has not asked for " + name + ", a paid search app. " + searchAppInstead
}

// searchAppInstead is what to do without a search app. It names no tool: a run's
// prompt says to research with web_search only when its device has it.
const searchAppInstead = "Research with the web tools you have instead, or ask the user how to proceed."
