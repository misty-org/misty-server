package api

import (
	"context"
	"regexp"

	"github.com/kannachi323/misty/server/internal/agenttools"
)

// Search apps are paid web search services the user would have to sign up
// for. Misty has its own web search, so an agent asks to connect one only when
// the user names it, or when Settings › Agents allows offering them.
var appsSearchServices = map[string]*regexp.Regexp{
	"exa":          regexp.MustCompile(`(?i)\bexa\b`),
	"tavily":       regexp.MustCompile(`(?i)\btavily\b`),
	"serpapi":      regexp.MustCompile(`(?i)\bserp ?api\b`),
	"serper":       regexp.MustCompile(`(?i)\bserper\b`),
	"perplexityai": regexp.MustCompile(`(?i)\bperplexity\b`),
	"firecrawl":    regexp.MustCompile(`(?i)\bfire ?crawl\b`),
}

type searchAppPolicy struct {
	// offer is the user's Offer search apps setting.
	offer bool
	// webSearch reports whether this run has Misty's web search.
	webSearch bool
	// request is what the user asked for in this run.
	request string
}

func (s *SpacesService) searchAppPolicy(ctx context.Context, invocation agenttools.Invocation) searchAppPolicy {
	values := s.agentPreferences(ctx, invocation.UserID)
	return searchAppPolicy{
		offer:     preferenceBool(values, prefOfferSearchApps, false),
		webSearch: s.models.Enabled() && preferenceBool(values, prefWebSearch, true),
		request:   invocation.OriginalInput,
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

// next is what apps_search tells the model about an unconnected app.
func (p searchAppPolicy) next(app string) string {
	connect := "Call apps_connect with app " + app + " before running its tools."
	if _, search := appsSearchServices[app]; !search || p.named(app) {
		return connect
	}
	instead := "Continue without web search, or ask the user how to proceed."
	if p.webSearch {
		instead = "Use web_search instead."
	}
	if p.offer {
		return "This is a paid search app the user would sign up for. " + instead + " Offer it only if that cannot find what the task needs; then " + connect
	}
	return "Do not connect this paid search app unless the user names it. " + instead
}

// refusal explains a blocked Connect card for a search app.
func (p searchAppPolicy) refusal(name string) string {
	message := "The user has not asked for " + name + ", a paid search app. "
	if p.webSearch {
		return message + "Use web_search instead."
	}
	return message + "Continue without web search, or ask the user how to proceed."
}
