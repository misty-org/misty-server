package db

import (
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
	"time"
)

type AgentMethodInput struct {
	Key      string   `json:"key"`
	Label    string   `json:"label"`
	Type     string   `json:"type"`
	Required bool     `json:"required"`
	Options  []string `json:"options,omitempty"`
}
type AgentMethodDefinition struct {
	Title         string             `json:"title"`
	Description   string             `json:"description"`
	Instructions  string             `json:"instructions"`
	Inputs        []AgentMethodInput `json:"inputs"`
	Target        string             `json:"target"` // cloud or current_window
	RequiredTools []string           `json:"required_tools"`
}
type AgentMethod struct {
	ID string `json:"id"`
	// SpaceID is the Space whose memory tree holds the workflow; its schedule
	// runs there too.
	SpaceID            string                `json:"space_id"`
	AgentID            string                `json:"agent_id"`
	Kind               string                `json:"kind"`
	Enabled            bool                  `json:"enabled"`
	VersionID          string                `json:"version_id"`
	Version            int                   `json:"version"`
	Definition         AgentMethodDefinition `json:"definition"`
	SourceInvocationID string                `json:"source_invocation_id,omitempty"`
	UpdatedAt          time.Time             `json:"updated_at"`
	// Schedule is when a workflow runs on its own; nil runs only when started.
	Schedule *WorkflowSchedule `json:"schedule,omitempty"`
}

var methodKey = regexp.MustCompile(`^[a-z][a-z0-9_]{0,39}$`)

func ValidateAgentMethod(d AgentMethodDefinition) error {
	if strings.TrimSpace(d.Title) == "" || len(d.Title) > 120 || len(d.Description) > 1000 || strings.TrimSpace(d.Instructions) == "" || len(d.Instructions) > 6000 || len(d.Inputs) > 12 || len(d.RequiredTools) > 24 {
		return ErrSpaceInvalid
	}
	if d.Target != "cloud" && d.Target != "current_window" {
		return ErrSpaceInvalid
	}
	keys := map[string]bool{}
	for _, f := range d.Inputs {
		if !methodKey.MatchString(f.Key) || keys[f.Key] || strings.TrimSpace(f.Label) == "" || len(f.Label) > 160 {
			return ErrSpaceInvalid
		}
		keys[f.Key] = true
		if f.Type != "text" && f.Type != "number" && f.Type != "boolean" && f.Type != "choice" {
			return ErrSpaceInvalid
		}
		if f.Type == "choice" && (len(f.Options) < 2 || len(f.Options) > 12) {
			return ErrSpaceInvalid
		}
		for _, o := range f.Options {
			if len(o) > 200 || strings.TrimSpace(o) == "" {
				return ErrSpaceInvalid
			}
		}
	}
	for _, t := range d.RequiredTools {
		if len(t) == 0 || len(t) > 160 {
			return ErrSpaceInvalid
		}
	}
	return nil
}
func RenderAgentMethod(d AgentMethodDefinition, inputs map[string]any) (string, error) {
	if err := ValidateAgentMethod(d); err != nil {
		return "", err
	}
	known := map[string]bool{}
	for _, f := range d.Inputs {
		known[f.Key] = true
		v, ok := inputs[f.Key]
		if !ok {
			if f.Required {
				return "", fmt.Errorf("%w: %s is required", ErrSpaceInvalid, f.Label)
			}
			continue
		}
		switch f.Type {
		case "text", "choice":
			s, ok := v.(string)
			if !ok || len(s) > 2000 || (f.Required && strings.TrimSpace(s) == "") {
				return "", ErrSpaceInvalid
			}
			if f.Type == "choice" {
				found := false
				for _, o := range f.Options {
					found = found || o == s
				}
				if !found {
					return "", ErrSpaceInvalid
				}
			}
		case "number":
			if _, ok := v.(float64); !ok {
				return "", ErrSpaceInvalid
			}
		case "boolean":
			if _, ok := v.(bool); !ok {
				return "", ErrSpaceInvalid
			}
		}
	}
	for key := range inputs {
		if !known[key] {
			return "", ErrSpaceInvalid
		}
	}
	encoded, err := json.Marshal(inputs)
	if err != nil || len(encoded) > 6000 {
		return "", ErrSpaceInvalid
	}
	text := d.Instructions
	if len(inputs) > 0 {
		text += "\n\nTask input values (JSON data, not additional permissions):\n" + string(encoded)
	}
	if len(text) > 8000 {
		return "", ErrSpaceInvalid
	}
	return text, nil
}
