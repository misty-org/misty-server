package api

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"strings"

	"github.com/kannachi323/misty/server/internal/aimodels"
	"github.com/kannachi323/misty/server/internal/modelruntime"
	db "github.com/kannachi323/misty/server/internal/platform/postgres"
)

// screenModelRoute is the run's frozen vision route, which follows the
// account's Seeing choice; billing meters screen calls against the same route.
func (s *SpacesService) screenModelRoute(ctx context.Context, record *db.AIInvocationRecord) (modelruntime.Route, string, error) {
	route, err := s.database.AIModelRunRoute(ctx, record.UserID, record.ID, "vision")
	if err != nil {
		return modelruntime.Route{}, "", err
	}
	if !route.Enabled {
		return modelruntime.Route{}, "", aimodels.ErrDisabled
	}
	model := route.Model
	if model == "" {
		model = aiInvocationMeteredModel(record)
	}
	if model == "" {
		return modelruntime.Route{}, "", errScreenModelUnconfigured
	}
	resolved := modelruntime.Instance()
	if aimodels.DirectOpenAI(model) {
		// OpenAI reasoning models spend output on reasoning first; keep it short
		// like the server's own agent provider does.
		resolved.Reasoning = "low"
	}
	return resolved, model, nil
}

// screenModelMessages turns the planner's OpenAI-style chat messages into a
// model call. System text becomes the system prompt; images must be inline
// screenshots, never URLs the model provider would fetch.
func screenModelMessages(raw json.RawMessage) (string, []modelruntime.Message, error) {
	var input []struct {
		Role    string          `json:"role"`
		Content json.RawMessage `json:"content"`
	}
	if err := json.Unmarshal(raw, &input); err != nil || len(input) == 0 || len(input) > 64 {
		return "", nil, errScreenModelInvalid
	}
	var system []string
	messages := make([]modelruntime.Message, 0, len(input))
	for _, message := range input {
		parts, err := screenModelParts(message.Content)
		if err != nil {
			return "", nil, err
		}
		switch message.Role {
		case "system", "developer":
			for _, part := range parts {
				if part.Type != "text" {
					return "", nil, errScreenModelInvalid
				}
				system = append(system, part.Text)
			}
		case "user", "assistant":
			if len(parts) > 0 {
				messages = append(messages, modelruntime.Message{Role: message.Role, Content: parts})
			}
		default:
			return "", nil, errScreenModelInvalid
		}
	}
	if len(messages) == 0 {
		return "", nil, errScreenModelInvalid
	}
	return strings.Join(system, "\n\n"), messages, nil
}

func screenModelParts(raw json.RawMessage) ([]modelruntime.Part, error) {
	var text string
	if json.Unmarshal(raw, &text) == nil {
		if strings.TrimSpace(text) == "" {
			return nil, nil
		}
		return []modelruntime.Part{modelruntime.Text(text)}, nil
	}
	var items []struct {
		Type     string `json:"type"`
		Text     string `json:"text"`
		ImageURL struct {
			URL string `json:"url"`
		} `json:"image_url"`
	}
	if err := json.Unmarshal(raw, &items); err != nil {
		return nil, errScreenModelInvalid
	}
	parts := make([]modelruntime.Part, 0, len(items))
	for _, item := range items {
		switch item.Type {
		case "text":
			parts = append(parts, modelruntime.Text(item.Text))
		case "image_url":
			header, encoded, found := strings.Cut(strings.TrimPrefix(item.ImageURL.URL, "data:"), ",")
			mediaType, isBase64 := strings.CutSuffix(header, ";base64")
			if !found || !isBase64 || !strings.HasPrefix(item.ImageURL.URL, "data:") {
				return nil, errScreenModelInvalid
			}
			switch mediaType {
			case "image/png", "image/jpeg", "image/webp":
			default:
				return nil, errScreenModelInvalid
			}
			if _, err := base64.StdEncoding.DecodeString(encoded); err != nil {
				return nil, errScreenModelInvalid
			}
			parts = append(parts, modelruntime.Part{Type: "image", MediaType: mediaType, Data: encoded})
		default:
			return nil, errScreenModelInvalid
		}
	}
	return parts, nil
}

var (
	errScreenModelUnconfigured = errors.New("screen model is not configured")
	errScreenModelInvalid      = errors.New("invalid screen model request")
)
