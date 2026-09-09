// Package browseractions adapts semantic capabilities to the existing leased
// browser primitives. It is a bounded executor, not a model or an agent loop.
package browseractions

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/url"
	"slices"
	"time"

	cap "github.com/kannachi323/misty/server/internal/capabilities"
)

var ErrStale = errors.New("browser_snapshot_stale")
var ErrUncertain = errors.New("browser_effect_uncertain")
var ErrReviewChanged = errors.New("browser_review_changed")
var ErrUnsupported = errors.New("browser_action_unsupported")

type Intervention struct{ Action, Reason string }

func (e *Intervention) Error() string { return e.Action + ": " + e.Reason }

type Element struct{ Ref, Tag, Role, Name string }
type Page struct {
	DocumentID  string    `json:"documentId"`
	URL         string    `json:"url"`
	Text        string    `json:"text"`
	Truncated   bool      `json:"truncated"`
	Interactive []Element `json:"interactive"`
	Target      struct {
		ScopeID        string `json:"scopeId"`
		ProfileID      string `json:"profileId"`
		Origin         string `json:"origin"`
		Authentication string `json:"authentication"`
		Trust          string `json:"trust"`
	} `json:"target"`
	Semantic Observation `json:"semantic"`
}

// Facts are extracted from provider account controls and identified content, not
// from model claims or arbitrary instructions in message bodies.
type Observation struct {
	Adapter    string      `json:"adapter"`
	Version    int         `json:"version"`
	Account    string      `json:"account"`
	Thread     string      `json:"thread,omitempty"`
	Subject    string      `json:"subject,omitempty"`
	Message    string      `json:"message,omitempty"`
	Text       string      `json:"text,omitempty"`
	Recipients []Recipient `json:"recipients,omitempty"`
	Draft      *Draft      `json:"draft,omitempty"`
	Task       *Task       `json:"task,omitempty"`
	Sent       *Draft      `json:"sent,omitempty"`
}
type Recipient struct {
	Address     string `json:"address"`
	DisplayName string `json:"displayName,omitempty"`
}
type Draft struct {
	Reference  string      `json:"reference"`
	Thread     string      `json:"thread"`
	Text       string      `json:"text"`
	Subject    string      `json:"subject"`
	Recipients []Recipient `json:"recipients"`
}
type Destination struct {
	TargetID           string `json:"targetId"`
	ContainerReference string `json:"containerReference"`
	Label              string `json:"label"`
}
type Source struct {
	Reference string `json:"reference"`
	Label     string `json:"label"`
}
type Task struct {
	Reference   string      `json:"taskReference,omitempty"`
	Destination Destination `json:"destination"`
	Title       string      `json:"title"`
	Text        string      `json:"text"`
	DueDate     string      `json:"dueDate,omitempty"`
	Source      Source      `json:"source"`
}

// Primitive is implemented by the server's existing device-job transport. Each
// call retains the admitted run, device, scope, lease and execution budget.
type Primitive func(context.Context, string, string, json.RawMessage) (json.RawMessage, error)
type Action struct {
	Operation string
	Input     map[string]any
}
type Prepared struct {
	BeforeReference string          `json:"beforeReference"`
	Account         string          `json:"account"`
	Input           json.RawMessage `json:"input"`
	Content         any             `json:"content"`
	// Binding participates in approval identity; profile/account/destination edits
	// cannot inherit a review made for a different target revision.
	Target cap.Target `json:"target"`
}

func (p Prepared) Hash() string {
	raw, _ := json.Marshal(p)
	raw, _ = cap.CanonicalJSON(raw)
	h := sha256.Sum256(raw)
	return hex.EncodeToString(h[:])
}
func ContentHash(account string, draft Draft) string {
	raw, _ := json.Marshal(struct {
		Account string
		Draft   Draft
	}{account, draft})
	h := sha256.Sum256(raw)
	return hex.EncodeToString(h[:])
}

// Adapters own preparation guidance and result verification. Registering another
// adapter changes neither this executor nor Ask's reasoning loop.
type Adapter interface {
	ID() string
	Version() int
	Supports(string) bool
	Origins() []string
	Prepare(Page, cap.Execution, cap.Target) (*Action, any, error)
	Commit(Page, cap.Execution, cap.Target) (*Action, error)
	Verify(Page, cap.Execution, Prepared) (json.RawMessage, bool, error)
}
type Registry struct{ adapters map[string]Adapter }

func NewRegistry(adapters ...Adapter) (*Registry, error) {
	r := &Registry{adapters: map[string]Adapter{}}
	for _, a := range adapters {
		key := key(a.ID(), a.Version())
		if a.ID() == "" || a.Version() < 1 || r.adapters[key] != nil {
			return nil, cap.ErrInvalid
		}
		r.adapters[key] = a
	}
	return r, nil
}
func key(id string, version int) string {
	raw, _ := json.Marshal([]any{id, version})
	return string(raw)
}
func (r *Registry) Resolve(provider cap.Provider, capability string) (Adapter, error) {
	a := r.adapters[key(provider.Route.Adapter, provider.Route.AdapterVersion)]
	if provider.Route.Kind != "browser" || a == nil || !a.Supports(capability) {
		return nil, ErrUnsupported
	}
	for _, origin := range provider.Route.Origins {
		if !slices.Contains(a.Origins(), origin) {
			return nil, ErrUnsupported
		}
	}
	return a, nil
}

type Session struct {
	Adapter   Adapter
	Target    cap.Target
	Binding   cap.BrowserBinding
	Execute   Primitive
	prefix    string
	sequence  int
	Attempted bool
}

func NewSession(adapter Adapter, target cap.Target, provider cap.Provider, execute Primitive, prefix string) (*Session, error) {
	b, err := target.ValidateBrowser(provider)
	if err != nil {
		return nil, err
	}
	if b.ScopeID == "" || b.AccountIdentity == "" {
		return nil, &Intervention{"account_confirmation", "Confirm the intended account and original browser view."}
	}
	return &Session{Adapter: adapter, Target: target, Binding: b, Execute: execute, prefix: prefix}, nil
}
func (s *Session) invoke(ctx context.Context, operation string, input map[string]any) (json.RawMessage, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	s.sequence++
	if s.sequence > 48 {
		return nil, errors.New("browser_action_budget_exceeded")
	}
	input["scopeId"] = s.Binding.ScopeID
	raw, _ := json.Marshal(input)
	suffix, _ := json.Marshal(s.sequence)
	return s.Execute(ctx, s.prefix+":"+string(suffix), operation, raw)
}
func (s *Session) Inspect(ctx context.Context) (Page, error) {
	var page Page
	raw, err := s.invoke(ctx, "browser.inspect", map[string]any{})
	if err != nil {
		return page, err
	}
	if json.Unmarshal(raw, &page) != nil {
		return page, cap.ErrInvalid
	}
	if page.Target.ScopeID != s.Binding.ScopeID || page.Target.ProfileID != s.Binding.ProfileID || page.Target.Trust != "host-observation" {
		return page, &Intervention{"account_confirmation", "The browser profile or view changed."}
	}
	if page.Target.Authentication == "required" {
		return page, &Intervention{"sign_in", "Sign in on the original browser view."}
	}
	u, err := url.Parse(page.URL)
	if err != nil || u.Scheme+"://"+u.Host != page.Target.Origin || !slices.Contains(s.Binding.Origins, page.Target.Origin) {
		return page, &Intervention{"open_target", "Return to the admitted provider origin."}
	}
	if page.Semantic.Adapter != s.Adapter.ID() || page.Semantic.Version != s.Adapter.Version() || page.Semantic.Account == "" || page.Semantic.Account != s.Binding.AccountIdentity {
		return page, &Intervention{"account_confirmation", "The observed provider account does not match the intended account."}
	}
	return page, nil
}
func (s *Session) act(ctx context.Context, page Page, action *Action) error {
	if action == nil {
		return cap.ErrInvalid
	}
	switch action.Operation {
	case "browser.click", "browser.type", "browser.interact", "browser.navigate":
	default:
		return ErrUnsupported
	}
	if action.Operation == "browser.navigate" {
		target, _ := action.Input["url"].(string)
		u, err := url.Parse(target)
		if err != nil || u.User != nil || !slices.Contains(s.Binding.Origins, u.Scheme+"://"+u.Host) {
			return ErrUnsupported
		}
	} else {
		action.Input["documentId"] = page.DocumentID
	}
	_, err := s.invoke(ctx, action.Operation, action.Input)
	if !errors.Is(err, ErrStale) {
		s.Attempted = true
	}
	return err
}
func (s *Session) Prepare(ctx context.Context, e cap.Execution) (Prepared, error) {
	for attempt := 0; attempt < 16; attempt++ {
		page, err := s.Inspect(ctx)
		if errors.Is(err, ErrStale) {
			continue
		}
		if err != nil {
			return Prepared{}, err
		}
		action, content, err := s.Adapter.Prepare(page, e, s.Target)
		if err != nil {
			return Prepared{}, err
		}
		if action == nil {
			return Prepared{BeforeReference: beforeReference(page), Account: page.Semantic.Account, Input: append(json.RawMessage(nil), e.Input...), Content: content, Target: s.Target}, nil
		}
		if err := s.act(ctx, page, action); err != nil && !errors.Is(err, ErrStale) {
			return Prepared{}, err
		}
	}
	return Prepared{}, ErrStale
}

// Commit never retries a write. After a lost response it only inspects and
// verifies; missing or ambiguous evidence remains uncertain and blocks work.
func (s *Session) Commit(ctx context.Context, e cap.Execution, review Prepared) (json.RawMessage, error) {
	for attempt := 0; attempt < 3; attempt++ {
		page, err := s.Inspect(ctx)
		if err != nil {
			return nil, err
		}
		action, content, err := s.Adapter.Prepare(page, e, s.Target)
		current := Prepared{BeforeReference: beforeReference(page), Account: page.Semantic.Account, Input: e.Input, Content: content, Target: s.Target}
		if err != nil || action != nil || current.Hash() != review.Hash() {
			return nil, ErrReviewChanged
		}
		commit, err := s.Adapter.Commit(page, e, s.Target)
		if err != nil {
			return nil, err
		}
		if commit != nil {
			err = s.act(ctx, page, commit)
			// Only a typed rejection proving that dispatch never started permits a
			// fresh inspection and retry. Transport failures go directly to reconciliation.
			if errors.Is(err, ErrStale) {
				continue
			}
			if ctx.Err() != nil {
				return nil, ErrUncertain
			}
		}
		return s.Reconcile(ctx, e, review)
	}
	return nil, ErrStale
}

func (s *Session) Reconcile(ctx context.Context, e cap.Execution, review Prepared) (json.RawMessage, error) {
	for attempt := 0; attempt < 3; attempt++ {
		page, err := s.Inspect(ctx)
		if err != nil {
			return nil, errors.Join(ErrUncertain, err)
		}
		raw, ok, err := s.Adapter.Verify(page, e, review)
		if err != nil {
			return nil, errors.Join(ErrUncertain, err)
		}
		if ok {
			return raw, nil
		}
		if attempt < 2 {
			select {
			case <-ctx.Done():
				return nil, ErrUncertain
			case <-time.After(150 * time.Millisecond):
			}
		}
	}
	return nil, ErrUncertain
}

func beforeReference(page Page) string {
	if page.Semantic.Sent != nil {
		return page.Semantic.Sent.Reference
	}
	if page.Semantic.Task != nil {
		return page.Semantic.Task.Reference
	}
	return ""
}
