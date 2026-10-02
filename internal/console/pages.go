package console

import (
	"context"
	"net/http"

	"github.com/a-h/templ"
)

// Page is one sidebar entry. Available gates it by deployment mode and
// environment; unavailable pages are hidden and their routes redirect to the
// Overview.
type Page struct {
	Slug      string
	Title     string
	Group     string
	Icon      func() templ.Component
	Available func(Config) bool
	Render    func(*Server, *http.Request) templ.Component
}

func always(Config) bool            { return true }
func developmentOnly(c Config) bool { return c.Development() }

// pages is the console's single navigation registry, in sidebar order. Each
// page has a unique icon; groups are separated by small muted headers.
var pages = []Page{
	{Slug: "overview", Title: "Overview", Icon: iconDashboard, Available: always, Render: (*Server).overviewPage},
	{Slug: "services", Title: "Services", Icon: iconBox, Available: developmentOnly, Render: (*Server).servicesPage},
	{Slug: "accounts", Title: "Accounts", Group: "People", Icon: iconUsers, Available: always, Render: (*Server).accountsPage},
	{Slug: "sync", Title: "Sync", Group: "People", Icon: iconRefresh, Available: always, Render: (*Server).syncPage},
	{Slug: "jobs", Title: "Jobs", Group: "Runtime", Icon: iconClock, Available: always, Render: (*Server).jobsPage},
	{Slug: "ai", Title: "AI", Group: "Runtime", Icon: iconSparkles, Available: always, Render: (*Server).aiPage},
	{Slug: "billing", Title: "Billing", Group: "Runtime", Icon: iconCard, Available: always, Render: (*Server).billingPage},
	{Slug: "configuration", Title: "Configuration", Group: "System", Icon: iconSliders, Available: always, Render: (*Server).configurationPage},
	{Slug: "database", Title: "Database", Group: "System", Icon: iconDatabase, Available: always, Render: (*Server).databasePage},
}

// NavGroup is a run of pages sharing a group header.
type NavGroup struct {
	Label string
	Pages []Page
}

func navigation(cfg Config) []NavGroup {
	var groups []NavGroup
	for _, page := range pages {
		if !page.Available(cfg) {
			continue
		}
		if len(groups) == 0 || groups[len(groups)-1].Label != page.Group {
			groups = append(groups, NavGroup{Label: page.Group})
		}
		groups[len(groups)-1].Pages = append(groups[len(groups)-1].Pages, page)
	}
	return groups
}

func findPage(cfg Config, slug string) (Page, bool) {
	for _, page := range pages {
		if page.Slug == slug && page.Available(cfg) {
			return page, true
		}
	}
	return Page{}, false
}

type sessionKey struct{}

func withSession(ctx context.Context, s session) context.Context {
	return context.WithValue(ctx, sessionKey{}, s)
}

func sessionFrom(ctx context.Context) session {
	s, _ := ctx.Value(sessionKey{}).(session)
	return s
}
