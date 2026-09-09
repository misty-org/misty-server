package browseractions

import (
	"encoding/json"
	"strings"
	"time"

	cap "github.com/kannachi323/misty/server/internal/capabilities"
)

type Pilot struct {
	Name           string
	AllowedOrigins []string
	Mail           bool
}

func (p Pilot) ID() string        { return p.Name }
func (p Pilot) Version() int      { return 1 }
func (p Pilot) Origins() []string { return p.AllowedOrigins }
func (p Pilot) Supports(name string) bool {
	if p.Mail {
		return name == "inbox.read" || name == "inbox.draft" || name == "inbox.send"
	}
	return name == "tasks.create"
}

var Pilots, _ = NewRegistry(
	Pilot{"gmail", []string{"https://mail.google.com"}, true},
	Pilot{"outlook", []string{"https://outlook.live.com", "https://outlook.office.com", "https://outlook.office365.com", "https://outlook.cloud.microsoft"}, true},
	Pilot{"todoist", []string{"https://app.todoist.com"}, false},
)

func (p Pilot) Prepare(page Page, e cap.Execution, target cap.Target) (*Action, any, error) {
	switch e.Capability {
	case "inbox.read":
		var input struct {
			Thread string `json:"threadReference"`
		}
		if json.Unmarshal(e.Input, &input) != nil || input.Thread == "" {
			return nil, nil, ErrUnsupported
		}
		if page.Semantic.Thread != input.Thread {
			return &Action{"browser.navigate", map[string]any{"url": input.Thread}}, nil, nil
		}
		if page.Semantic.Message == "" || page.Truncated {
			return nil, nil, ErrUnsupported
		}
		return nil, page.Semantic, nil
	case "inbox.draft":
		var input struct {
			Recipients  []Recipient       `json:"recipients"`
			Subject     string            `json:"subject"`
			Text        string            `json:"text"`
			ReplyTo     string            `json:"replyTo"`
			Attachments []json.RawMessage `json:"attachments"`
		}
		if json.Unmarshal(e.Input, &input) != nil || input.ReplyTo == "" || len(input.Attachments) > 0 {
			return nil, nil, ErrUnsupported
		}
		if page.Semantic.Thread != input.ReplyTo {
			return &Action{"browser.navigate", map[string]any{"url": input.ReplyTo}}, nil, nil
		}
		draft := page.Semantic.Draft
		if draft == nil {
			return control(page, "Reply", "browser.click", nil)
		}
		if draft.Thread != input.ReplyTo || !same(draft.Recipients, input.Recipients) {
			return nil, nil, &Intervention{"review", "The reply thread or recipients differ from the requested reply."}
		}
		if draft.Text != input.Text {
			return control(page, "Message Body", "browser.interact", map[string]any{"kind": "fill", "text": input.Text})
		}
		if input.Subject != "" && draft.Subject != input.Subject {
			return nil, nil, ErrReviewChanged
		}
		return nil, *draft, nil
	case "inbox.send":
		var input struct {
			Reference  string      `json:"draftReference"`
			Hash       string      `json:"expectedContentHash"`
			Recipients []Recipient `json:"recipients"`
		}
		if json.Unmarshal(e.Input, &input) != nil {
			return nil, nil, cap.ErrInvalid
		}
		draft := page.Semantic.Draft
		if draft == nil || draft.Reference != input.Reference || !same(draft.Recipients, input.Recipients) || ContentHash(page.Semantic.Account, *draft) != input.Hash {
			return nil, nil, ErrReviewChanged
		}
		return nil, *draft, nil
	case "tasks.create":
		var input Task
		if json.Unmarshal(e.Input, &input) != nil || input.Destination.TargetID != target.ID {
			return nil, nil, cap.ErrInvalid
		}
		draft := page.Semantic.Task
		if draft == nil {
			return control(page, "Add task", "browser.click", nil)
		}
		if draft.Reference != "" {
			return nil, nil, ErrReviewChanged
		}
		if draft.Destination.ContainerReference != input.Destination.ContainerReference {
			return &Action{"browser.navigate", map[string]any{"url": input.Destination.ContainerReference}}, nil, nil
		}
		if draft.Destination.Label != input.Destination.Label {
			return nil, nil, ErrReviewChanged
		}
		if draft.Title != input.Title {
			return control(page, "Task name", "browser.interact", map[string]any{"kind": "fill", "text": input.Title})
		}
		description := input.Text + "\n\n" + input.Source.Label + ": " + input.Source.Reference
		if draft.Text != description {
			return control(page, "Description", "browser.interact", map[string]any{"kind": "fill", "text": description})
		}
		if draft.DueDate != input.DueDate {
			return control(page, "Due date", "browser.interact", map[string]any{"kind": "fill", "text": input.DueDate})
		}
		return nil, input, nil
	}
	return nil, nil, ErrUnsupported
}
func control(page Page, name, operation string, action map[string]any) (*Action, any, error) {
	var found *Element
	for _, element := range page.Interactive {
		if strings.EqualFold(element.Name, name) {
			if found != nil {
				return nil, nil, ErrUnsupported
			}
			copy := element
			found = &copy
		}
	}
	if found == nil {
		return nil, nil, ErrUnsupported
	}
	input := map[string]any{"elementRef": found.Ref}
	if action != nil {
		action["elementRef"] = found.Ref
		input = map[string]any{"action": action}
	}
	return &Action{operation, input}, nil, nil
}
func (p Pilot) Commit(page Page, e cap.Execution, target cap.Target) (*Action, error) {
	name := ""
	switch e.Capability {
	case "inbox.read", "inbox.draft":
		return nil, nil
	case "inbox.send":
		name = "Send"
	case "tasks.create":
		name = "Add task"
	default:
		return nil, ErrUnsupported
	}
	action, _, err := control(page, name, "browser.click", nil)
	return action, err
}
func same(a, b any) bool {
	ra, _ := json.Marshal(a)
	rb, _ := json.Marshal(b)
	return cap.EqualJSON(ra, rb)
}
func (p Pilot) Verify(page Page, e cap.Execution, review Prepared) (json.RawMessage, bool, error) {
	now := time.Now().UTC().Format(time.RFC3339Nano)
	evidence := []any{map[string]any{"targetId": e.TargetID, "observedAt": now, "kind": "browser", "reference": page.URL, "revision": page.DocumentID}}
	var result any
	switch e.Capability {
	case "inbox.read":
		if !same(review.Content, page.Semantic) {
			return nil, false, nil
		}
		o := page.Semantic
		result = map[string]any{"accountIdentity": page.Semantic.Account, "sourceTargetId": e.TargetID, "messages": []any{map[string]any{"reference": o.Message, "threadReference": o.Thread, "subject": o.Subject, "recipients": o.Recipients, "text": o.Text, "observedAt": now, "attachments": []any{}, "truncated": page.Truncated}}, "coverage": "opened_thread", "partial": false, "truncated": page.Truncated, "limitations": []any{}, "evidence": evidence}
	case "inbox.draft":
		draft := page.Semantic.Draft
		if draft == nil || !same(review.Content, *draft) {
			return nil, false, nil
		}
		result = map[string]any{"accountIdentity": page.Semantic.Account, "sourceTargetId": e.TargetID, "draftReference": draft.Reference, "threadReference": draft.Thread, "contentHash": ContentHash(page.Semantic.Account, *draft), "recipients": draft.Recipients, "evidence": evidence}
	case "inbox.send":
		sent := page.Semantic.Sent
		if sent == nil {
			return nil, false, nil
		}
		var approved Draft
		raw, _ := json.Marshal(review.Content)
		if json.Unmarshal(raw, &approved) != nil {
			return nil, false, cap.ErrInvalid
		}
		// Sending changes the provider's draft reference, not the reviewed body,
		// thread, subject or recipients. Require an identified observed sent item.
		if sent.Reference == "" || sent.Reference == review.BeforeReference || sent.Thread != approved.Thread || sent.Text != approved.Text || sent.Subject != approved.Subject || !same(sent.Recipients, approved.Recipients) {
			return nil, false, nil
		}
		result = map[string]any{"accountIdentity": page.Semantic.Account, "sourceTargetId": e.TargetID, "messageReference": sent.Reference, "threadReference": sent.Thread, "confirmation": "observed_sent_item", "recipients": sent.Recipients, "evidence": evidence}
	case "tasks.create":
		task := page.Semantic.Task
		var input Task
		if json.Unmarshal(e.Input, &input) != nil {
			return nil, false, cap.ErrInvalid
		}
		if task == nil || task.Reference == "" || task.Reference == review.BeforeReference || task.Title != input.Title || task.Text != input.Text+"\n\n"+input.Source.Label+": "+input.Source.Reference || task.DueDate != input.DueDate || task.Destination.ContainerReference != input.Destination.ContainerReference {
			return nil, false, nil
		}
		result = map[string]any{"taskReference": task.Reference, "destination": input.Destination, "title": input.Title, "text": input.Text, "source": input.Source, "evidence": evidence}
		if input.DueDate != "" {
			result.(map[string]any)["dueDate"] = input.DueDate
		}
	default:
		return nil, false, ErrUnsupported
	}
	raw, _ := json.Marshal(map[string]any{"status": "success", "result": result, "evidence": evidence, "partial": false})
	return raw, true, nil
}
