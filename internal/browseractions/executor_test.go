package browseractions

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	cap "github.com/kannachi323/misty/server/internal/capabilities"
)

type fixture struct {
	page        Page
	writes      int
	lost        bool
	stale       bool
	unconfirmed bool
}

func (f *fixture) execute(ctx context.Context, _ string, operation string, input json.RawMessage) (json.RawMessage, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if operation == "browser.inspect" {
		f.page.DocumentID = uuid.NewString()
		return json.Marshal(f.page)
	}
	if f.stale {
		f.stale = false
		return nil, ErrStale
	}
	if operation != "browser.click" {
		return nil, ErrUnsupported
	}
	f.writes++
	if !f.unconfirmed {
		if f.page.Semantic.Draft != nil {
			sent := *f.page.Semantic.Draft
			sent.Reference = "https://mail.google.com/#sent/new-message"
			f.page.Semantic.Sent = &sent
			f.page.Semantic.Draft = nil
		}
		if f.page.Semantic.Task != nil {
			f.page.Semantic.Task.Reference = "https://app.todoist.com/app/task/verified-1"
		}
	}
	if f.lost {
		return nil, errors.New("lost response")
	}
	return json.RawMessage(`{"ok":true}`), nil
}
func setup(t *testing.T, name string) (*Session, *fixture, cap.Execution) {
	t.Helper()
	mail := name != "todoist"
	origin := "https://mail.google.com"
	if name == "outlook" {
		origin = "https://outlook.live.com"
	}
	if !mail {
		origin = "https://app.todoist.com"
	}
	adapter := Pilot{name, []string{origin}, mail}
	provider := cap.Provider{ID: "example.pilot/" + name, Version: 1, Route: cap.Route{Kind: "browser", Adapter: name, AdapterVersion: 1, Origins: []string{origin}}}
	binding := cap.BrowserBinding{Kind: "browser", DeviceID: uuid.NewString(), ProfileID: strings.Repeat("a", 64), AccountBindingID: uuid.NewString(), Origins: []string{origin}, ScopeID: "browser-pilot", AccountIdentity: "owner@example.com"}
	raw, _ := json.Marshal(binding)
	target := cap.Target{ID: uuid.NewString(), Revision: 1, AppID: "example.pilot", ProviderID: provider.ID, ProviderVersion: 1, Label: "Pilot", Binding: raw}
	f := &fixture{}
	f.page.URL = origin + "/#thread/1"
	f.page.Target.ScopeID = binding.ScopeID
	f.page.Target.ProfileID = binding.ProfileID
	f.page.Target.Origin = origin
	f.page.Target.Trust = "host-observation"
	f.page.Semantic = Observation{Adapter: name, Version: 1, Account: binding.AccountIdentity, Thread: f.page.URL}
	f.page.Interactive = []Element{{Ref: "commit", Role: "button", Name: "Send"}}
	e := cap.Execution{Invocation: cap.Invocation{RequestID: uuid.NewString(), Capability: "inbox.send", CapabilityVersion: 1, ProviderID: provider.ID, ProviderVersion: 1, TargetID: target.ID, TargetRevision: 1, Deadline: time.Now().Add(time.Hour)}, RunID: uuid.NewString(), EffectID: uuid.NewString(), GrantIDs: []string{}}
	draft := Draft{Reference: origin + "/#draft/1", Thread: f.page.URL, Text: "Available Tuesday", Subject: "Availability", Recipients: []Recipient{{Address: "boss@example.com"}}}
	f.page.Semantic.Draft = &draft
	e.Input, _ = json.Marshal(map[string]any{"draftReference": draft.Reference, "expectedContentHash": ContentHash(binding.AccountIdentity, draft), "recipients": draft.Recipients})
	if !mail {
		e.Capability = "tasks.create"
		task := Task{Destination: Destination{target.ID, origin + "/app/project/1", "Work"}, Title: "Reply", Text: "Follow up", Source: Source{"https://mail.google.com/#thread/1", "Email"}}
		e.Input, _ = json.Marshal(task)
		prepared := task
		prepared.Text = task.Text + "\n\n" + task.Source.Label + ": " + task.Source.Reference
		f.page.Semantic.Draft = nil
		f.page.Semantic.Task = &prepared
		f.page.Interactive[0].Name = "Add task"
	}
	s, err := NewSession(adapter, target, provider, f.execute, "fixture")
	if err != nil {
		t.Fatal(err)
	}
	return s, f, e
}
func TestInterchangeableProvidersVerifyThroughOneExecutor(t *testing.T) {
	for _, name := range []string{"gmail", "outlook", "todoist", "fixture_mail"} {
		t.Run(name, func(t *testing.T) {
			s, f, e := setup(t, name)
			r, err := NewRegistry(s.Adapter)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := r.Resolve(cap.Provider{Route: cap.Route{Kind: "browser", Adapter: name, AdapterVersion: 1, Origins: s.Adapter.Origins()}}, e.Capability); err != nil {
				t.Fatal(err)
			}
			prepared, err := s.Prepare(t.Context(), e)
			if err != nil {
				t.Fatal(err)
			}
			if f.writes != 0 {
				t.Fatal("preparation committed effect")
			}
			raw, err := s.Commit(t.Context(), e, prepared)
			if err != nil {
				t.Fatal(err)
			}
			var outcome cap.BackendOutcome
			if json.Unmarshal(raw, &outcome) != nil || outcome.Status != "success" || f.writes != 1 {
				t.Fatalf("unverified result: %s writes=%d", raw, f.writes)
			}
		})
	}
}
func TestLostResponseReconcilesWithoutRepeatingWrite(t *testing.T) {
	for _, name := range []string{"gmail", "outlook", "todoist"} {
		t.Run(name, func(t *testing.T) {
			s, f, e := setup(t, name)
			p, err := s.Prepare(t.Context(), e)
			if err != nil {
				t.Fatal(err)
			}
			f.lost = true
			if _, err := s.Commit(t.Context(), e, p); err != nil {
				t.Fatal(err)
			}
			if f.writes != 1 {
				t.Fatal("duplicate write")
			}
			if _, err := s.Reconcile(t.Context(), e, p); err != nil {
				t.Fatal(err)
			}
			if f.writes != 1 {
				t.Fatal("reconciliation wrote")
			}
		})
	}
}
func TestChangedReviewOrAccountCannotCommit(t *testing.T) {
	for _, change := range []string{"account", "profile", "scope", "origin", "recipients", "content", "draft", "cancel"} {
		t.Run(change, func(t *testing.T) {
			s, f, e := setup(t, "gmail")
			p, err := s.Prepare(t.Context(), e)
			if err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			switch change {
			case "account":
				f.page.Semantic.Account = "other@example.com"
			case "profile":
				f.page.Target.ProfileID = strings.Repeat("b", 64)
			case "scope":
				f.page.Target.ScopeID = "different"
			case "origin":
				f.page.Target.Origin = "https://evil.invalid"
			case "recipients":
				f.page.Semantic.Draft.Recipients[0].Address = "other@example.com"
			case "content":
				f.page.Semantic.Draft.Text = "Changed"
			case "draft":
				f.page.Semantic.Draft.Reference = "different"
			case "cancel":
				cancel()
			case "stale":
				f.stale = true
			}
			if _, err := s.Commit(ctx, e, p); err == nil {
				t.Fatal("changed review committed")
			}
			if f.writes != 0 {
				t.Fatal("write after invalidation")
			}
		})
	}
}
func TestUnconfirmedWriteRemainsUncertain(t *testing.T) {
	s, f, e := setup(t, "gmail")
	p, err := s.Prepare(t.Context(), e)
	if err != nil {
		t.Fatal(err)
	}
	f.lost = true
	f.unconfirmed = true
	if _, err := s.Commit(t.Context(), e, p); !errors.Is(err, ErrUncertain) {
		t.Fatalf("uncertainty lost: %v", err)
	}
	if f.writes != 1 {
		t.Fatal("duplicate")
	}
	if _, err := s.Reconcile(t.Context(), e, p); !errors.Is(err, ErrUncertain) {
		t.Fatal(err)
	}
	if f.writes != 1 {
		t.Fatal("reconciliation repeated send")
	}
}
func TestExpiredLoginRequiresIntervention(t *testing.T) {
	s, f, e := setup(t, "gmail")
	f.page.Target.Authentication = "required"
	_, err := s.Prepare(t.Context(), e)
	var intervention *Intervention
	if !errors.As(err, &intervention) || intervention.Action != "sign_in" {
		t.Fatal(err)
	}
	if f.writes != 0 {
		t.Fatal("wrote during login")
	}
}

func TestStalePageReinspectsBeforeSingleCommit(t *testing.T) {
	s, f, e := setup(t, "gmail")
	p, err := s.Prepare(t.Context(), e)
	if err != nil {
		t.Fatal(err)
	}
	f.stale = true
	if _, err := s.Commit(t.Context(), e, p); err != nil {
		t.Fatal(err)
	}
	if f.writes != 1 {
		t.Fatal("stale recovery repeated commit")
	}
}

func TestPreparedDigestSurvivesDurableJSONRoundTrip(t *testing.T) {
 s,_,e:=setup(t,"gmail");original,err:=s.Prepare(t.Context(),e);if err!=nil{t.Fatal(err)}
 raw,_:=json.Marshal(original);var restored Prepared;if err:=json.Unmarshal(raw,&restored);err!=nil{t.Fatal(err)}
 if original.Hash()!=restored.Hash(){t.Fatal("durable review changed without a content change")}
}
