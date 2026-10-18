package agent

import (
	"context"
	"errors"
	"sort"
	"strings"
	"sync"
	"time"
)

// SenseModel is one Gateway model an account can choose for a sense.
type SenseModel struct {
	ID       string `json:"id"`
	Name     string `json:"name"`
	Provider string `json:"provider"`
	// PaidOnly marks a model the account's plan cannot use (set by the API).
	PaidOnly bool `json:"paid_only,omitempty"`
	// More marks a model outside each lab's current lineup; pickers tuck it behind "More models".
	More bool `json:"more,omitempty"`
	// released is when the maker released the model; zero when the Gateway doesn't say.
	released time.Time
}

var senseCatalogCache struct {
	sync.Mutex
	items     []gatewayCatalogItem
	expiresAt time.Time
}

// SenseModels lists the Gateway models that fit a sense: tool-using language
// models think, image-reading ones see, transcription models listen and
// realtime voices speak.
func SenseModels(ctx context.Context, kind string) ([]SenseModel, error) {
	items, err := senseCatalog(ctx)
	if err != nil {
		return nil, err
	}
	out := []SenseModel{}
	for _, item := range items {
		// Billing charges the Gateway's published price; a model without one
		// could never be admitted, so it is never offered.
		if !senseFits(kind, item) || item.Pricing == nil || strings.TrimSpace(item.Pricing.Input) == "" && strings.TrimSpace(item.Pricing.Output) == "" && item.Type == "language" {
			continue
		}
		_, provider := frontierProvider(item.ID)
		name := strings.TrimSpace(item.Name)
		if name == "" {
			name = modelDisplayName(item.ID)
		}
		model := SenseModel{ID: item.ID, Name: name, Provider: provider}
		if item.Released > 0 {
			model.released = time.Unix(item.Released, 0)
		}
		out = append(out, model)
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Provider == out[j].Provider {
			return strings.ToLower(out[i].Name) < strings.ToLower(out[j].Name)
		}
		return out[i].Provider < out[j].Provider
	})
	return out, nil
}

// pickerModelsSince is the oldest release a model picker offers for Thinking. Older models
// are weaker at agent work, and picking among them is noise.
var pickerModelsSince = time.Date(2026, time.January, 1, 0, 0, 0, 0, time.UTC)

// serviceTierTokens in a model's id mark the same model sold on another service tier (batch,
// priority or high-speed serving), or a "fast" line, wherever they sit in the id.
var serviceTierTokens = map[string]bool{"batch": true, "fast": true, "highspeed": true, "ultraspeed": true}

func isServiceTier(id string) bool {
	_, rest, found := strings.Cut(strings.ToLower(id), "/")
	if !found {
		rest = strings.ToLower(id)
	}
	for _, token := range strings.Split(rest, "-") {
		if serviceTierTokens[token] {
			return true
		}
	}
	return false
}

// modelFamily names the line a model belongs to by dropping its version numbers and dates:
// openai/gpt-5.6-luna and openai/gpt-6-luna are both "openai/gpt-luna", while
// anthropic/claude-opus-5.5 and anthropic/claude-sonnet-5.5 stay apart.
func modelFamily(id string) string {
	provider, rest, found := strings.Cut(strings.ToLower(id), "/")
	if !found {
		provider, rest = "", provider
	}
	parts := []string{}
	for _, token := range strings.Split(rest, "-") {
		token = strings.Map(func(r rune) rune {
			if (r >= '0' && r <= '9') || r == '.' {
				return -1
			}
			return r
		}, token)
		if token != "" && token != "v" {
			parts = append(parts, token)
		}
	}
	return provider + "/" + strings.Join(parts, "-")
}

// prereleaseTokens in a model's family mark free tiers, open instruct builds and betas, which
// no lab leads with; they sit behind "More models".
var prereleaseTokens = map[string]bool{"beta": true, "free": true, "it": true, "exp": true}

// currentWindow is how far behind its lab's newest release a line may fall and still count
// as current; a line the lab has moved well past is no longer on its menu.
const currentWindow = 4 * 30 * 24 * time.Hour

func isPrerelease(id string) bool {
	_, line, _ := strings.Cut(modelFamily(id), "/")
	for _, token := range strings.Split(line, "-") {
		if prereleaseTokens[token] {
			return true
		}
	}
	return false
}

// PickerModels trims the models a picker offers. Thinking lists only models released since
// 2026, each once rather than once per service tier, and marks everything but each lab's
// current lineup as More, for the picker to tuck behind "More models": the newest model in
// each of the lab's lines, for lines the lab has not moved well past, without free tiers,
// open instruct builds or betas. The other senses list everything. The model currently
// chosen (keep) is never hidden or marked More. Models marked More or hidden remain valid:
// SenseModelAvailable still accepts them.
func PickerModels(kind string, models []SenseModel, keep string) []SenseModel {
	if kind != "thinking" {
		return models
	}
	out := make([]SenseModel, 0, len(models))
	familyNewest := map[string]time.Time{}
	providerNewest := map[string]time.Time{}
	for _, model := range models {
		old := !model.released.IsZero() && model.released.Before(pickerModelsSince)
		if model.ID != keep && (old || isServiceTier(model.ID)) {
			continue
		}
		out = append(out, model)
		provider, _, _ := strings.Cut(model.ID, "/")
		if model.released.After(familyNewest[modelFamily(model.ID)]) {
			familyNewest[modelFamily(model.ID)] = model.released
		}
		if model.released.After(providerNewest[provider]) {
			providerNewest[provider] = model.released
		}
	}
	for i, model := range out {
		provider, _, _ := strings.Cut(model.ID, "/")
		// A model with no release date can't be compared, so it is never tucked away for age.
		current := model.released.IsZero() ||
			(!model.released.Before(familyNewest[modelFamily(model.ID)]) &&
				model.released.After(providerNewest[provider].Add(-currentWindow)))
		out[i].More = model.ID != keep && !(current && !isPrerelease(model.ID))
	}
	return out
}

// SenseModelAvailable reports whether a model may be chosen for a sense.
func SenseModelAvailable(ctx context.Context, kind, modelID string) bool {
	models, err := SenseModels(ctx, kind)
	if err != nil {
		return false
	}
	for _, model := range models {
		if model.ID == modelID {
			return true
		}
	}
	return false
}

func senseFits(kind string, item gatewayCatalogItem) bool {
	tags := map[string]bool{}
	for _, tag := range item.Tags {
		tags[strings.ToLower(strings.TrimSpace(tag))] = true
	}
	switch strings.ToLower(strings.TrimSpace(item.Type)) {
	case "language":
		switch kind {
		case "thinking":
			return tags["tool-use"]
		case "seeing":
			return tags["vision"] && tags["structured-output"]
		}
	case "transcription":
		// Batch transcription only; streaming-only models need a socket.
		return kind == "listening" && !tags["websocket-transcription"] && !tags["websocket-realtime"]
	case "realtime":
		return kind == "speaking"
	}
	return false
}

func senseCatalog(ctx context.Context) ([]gatewayCatalogItem, error) {
	senseCatalogCache.Lock()
	if time.Now().Before(senseCatalogCache.expiresAt) && len(senseCatalogCache.items) > 0 {
		items := senseCatalogCache.items
		senseCatalogCache.Unlock()
		return items, nil
	}
	senseCatalogCache.Unlock()
	items, err := fetchGatewayCatalog(ctx)
	if err != nil {
		return nil, err
	}
	if len(items) == 0 {
		return nil, errors.New("gateway model catalog is empty")
	}
	senseCatalogCache.Lock()
	senseCatalogCache.items, senseCatalogCache.expiresAt = items, time.Now().Add(10*time.Minute)
	senseCatalogCache.Unlock()
	return items, nil
}

// SenseModelRetired reports whether the Gateway no longer lists a model for a
// sense. An unreachable catalog never retires anything.
func SenseModelRetired(ctx context.Context, kind, modelID string) bool {
	models, err := SenseModels(ctx, kind)
	if err != nil {
		return false
	}
	for _, model := range models {
		if model.ID == modelID {
			return false
		}
	}
	return true
}
