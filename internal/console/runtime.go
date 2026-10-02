package console

import (
	"context"
	"net/http"
	"strings"
	"time"

	"github.com/a-h/templ"

	db "github.com/kannachi323/misty/server/internal/platform/postgres"
)

type syncView struct {
	Vaults []db.ConsoleSyncVault
	Totals db.ConsoleSyncTotals
	Err    string
}

func (s *Server) syncPage(r *http.Request) templ.Component {
	if s.db == nil {
		return databaseUnavailable("Sync")
	}
	vaults, totals, err := s.db.ConsoleSyncVaults(r.Context(), 100)
	view := syncView{Vaults: vaults, Totals: totals}
	if err != nil {
		view.Err = "Couldn't load sync state: " + err.Error()
	}
	return syncPage(view)
}

type jobsView struct {
	Counts []db.ConsoleStateCount
	Jobs   []db.ConsoleJob
	Err    string
}

// countGroups groups state counts under their job kind, preserving order.
func countGroups(counts []db.ConsoleStateCount) [][]db.ConsoleStateCount {
	var groups [][]db.ConsoleStateCount
	for _, c := range counts {
		if len(groups) == 0 || groups[len(groups)-1][0].Kind != c.Kind {
			groups = append(groups, nil)
		}
		groups[len(groups)-1] = append(groups[len(groups)-1], c)
	}
	return groups
}

func (s *Server) jobsPage(r *http.Request) templ.Component {
	if s.db == nil {
		return databaseUnavailable("Jobs")
	}
	counts, jobs, err := s.db.ConsoleJobs(r.Context(), 50)
	view := jobsView{Counts: counts, Jobs: jobs}
	if err != nil {
		view.Err = "Couldn't load jobs: " + err.Error()
	}
	return jobsPage(view)
}

// setting is one configuration value shown with secrets masked.
type setting struct {
	Name     string
	Value    string
	Set      bool
	Secret   bool
	Required bool
}

func (s setting) display() string {
	switch {
	case !s.Set:
		return "not set"
	case s.Secret:
		return "set · hidden"
	case len(s.Value) > 64:
		return s.Value[:61] + "…"
	default:
		return s.Value
	}
}

func isSecretName(name string) bool {
	for _, marker := range []string{"KEY", "SECRET", "TOKEN", "PASSWORD", "PEPPER", "PRIVATE", "CREDENTIAL", "DSN"} {
		if strings.Contains(name, marker) {
			return true
		}
	}
	return false
}

func (s *Server) setting(name string, required bool) setting {
	value, ok := s.cfg.lookup(name)
	return setting{Name: name, Value: value, Set: ok && value != "", Secret: isSecretName(name), Required: required}
}

type aiView struct {
	Config   Config
	Model    []setting
	Gateway  *HealthCheck
	States   []db.ConsoleStateCount
	Failures []db.ConsoleStateCount
	Err      string
}

func (s *Server) aiPage(r *http.Request) templ.Component {
	view := aiView{Config: s.cfg}
	view.Model = []setting{s.setting("AI_GATEWAY_API_KEY", true)}
	ctx, cancel := context.WithTimeout(r.Context(), 6*time.Second)
	defer cancel()
	if snapshot, err := s.health.Fetch(ctx); err == nil {
		if check, ok := snapshot.Checks["agent_gateway"]; ok {
			check.Name = "agent_gateway"
			view.Gateway = &check
		}
	}
	if s.db == nil {
		view.Err = "Database unavailable, so usage can't be shown."
		return aiPage(view)
	}
	states, failures, err := s.db.ConsoleAIUsage(ctx)
	view.States, view.Failures = states, failures
	if err != nil {
		view.Err = "Couldn't load AI usage: " + err.Error()
	}
	return aiPage(view)
}

type billingView struct {
	Billing db.ConsoleBilling
	Check   *HealthCheck
	Err     string
}

func (s *Server) billingPage(r *http.Request) templ.Component {
	if s.db == nil {
		return databaseUnavailable("Billing")
	}
	ctx, cancel := context.WithTimeout(r.Context(), 6*time.Second)
	defer cancel()
	view := billingView{}
	if snapshot, err := s.health.Fetch(ctx); err == nil {
		if check, ok := snapshot.Checks["billing"]; ok {
			check.Name = "billing"
			view.Check = &check
		}
	}
	billing, err := s.db.ConsoleBilling(ctx)
	view.Billing = billing
	if err != nil {
		view.Err = "Couldn't load billing state: " + err.Error()
	}
	return billingPage(view)
}
