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
		if !senseFits(kind, item) {
			continue
		}
		_, provider := frontierProvider(item.ID)
		name := strings.TrimSpace(item.Name)
		if name == "" {
			name = modelDisplayName(item.ID)
		}
		out = append(out, SenseModel{ID: item.ID, Name: name, Provider: provider})
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Provider == out[j].Provider {
			return strings.ToLower(out[i].Name) < strings.ToLower(out[j].Name)
		}
		return out[i].Provider < out[j].Provider
	})
	return out, nil
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
