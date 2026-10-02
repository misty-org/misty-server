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
}

func (p Pilot) ID() string                { return p.Name }
func (p Pilot) Version() int              { return 1 }
func (p Pilot) Origins() []string         { return p.AllowedOrigins }
func (p Pilot) Supports(name string) bool { return name == "tasks.create" }

var Pilots, _ = NewRegistry(
	Pilot{"todoist", []string{"https://app.todoist.com"}},
)

func (p Pilot) Prepare(page Page, e cap.Execution, target cap.Target) (*Action, any, error) {
	switch e.Capability {
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
	case "tasks.create":
		name = "Add task"
	default:
		return nil, ErrUnsupported
	}
	action, _, err := control(page, name, "browser.click", nil)
	return action, err
}
func (p Pilot) Verify(page Page, e cap.Execution, review Prepared) (json.RawMessage, bool, error) {
	now := time.Now().UTC().Format(time.RFC3339Nano)
	evidence := []any{map[string]any{"targetId": e.TargetID, "observedAt": now, "kind": "browser", "reference": page.URL, "revision": page.DocumentID}}
	var result any
	switch e.Capability {
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
