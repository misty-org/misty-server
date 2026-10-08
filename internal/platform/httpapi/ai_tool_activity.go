package api

import (
	"encoding/json"
	"fmt"
	"net/url"
	"strings"
)

// aiToolActivity is what a reply's timeline shows about one tool call: its
// target, a short result, and up to a few rows or lines of what it got back.
// It is built from the call's arguments and result by known field names only,
// so tokens, page text and other raw payloads never reach the conversation.
type aiToolActivity struct {
	Detail string              `json:"detail,omitempty"`
	Result string              `json:"result,omitempty"`
	Rows   []aiToolActivityRow `json:"rows,omitempty"`
	Output string              `json:"output,omitempty"`
}

type aiToolActivityRow struct {
	Title string `json:"title"`
	Meta  string `json:"meta,omitempty"`
}

const (
	maxActivityRows   = 6
	maxActivityText   = 120
	maxActivityOutput = 1200
)

// Argument fields that name a call's target, most specific first. Typed text
// and prompts are left out: a page field may hold something private.
var activityDetailKeys = []string{"query", "q", "url", "title", "name", "path", "command"}

// Result fields that hold a list of found or changed things.
var activityListKeys = []string{"results", "items", "matches", "sources", "pages", "documents", "files", "tabs", "rows", "entries"}

// Result fields that hold a command's or check's printed output.
var activityOutputKeys = []string{"stdout", "output", "text_output"}

func toolActivity(arguments, result json.RawMessage) *aiToolActivity {
	activity := aiToolActivity{Detail: activityDetail(arguments)}
	var value map[string]any
	if json.Unmarshal(result, &value) == nil {
		for _, key := range activityListKeys {
			list, ok := value[key].([]any)
			if !ok {
				continue
			}
			activity.Result = countLabel(len(list), "result")
			for _, entry := range list {
				if row, ok := activityRow(entry); ok && len(activity.Rows) < maxActivityRows {
					activity.Rows = append(activity.Rows, row)
				}
			}
			break
		}
		for _, key := range activityOutputKeys {
			if text, ok := value[key].(string); ok && strings.TrimSpace(text) != "" {
				activity.Output = clipActivity(strings.TrimSpace(text), maxActivityOutput)
				break
			}
		}
		if activity.Result == "" {
			if count, ok := value["count"].(float64); ok {
				activity.Result = countLabel(int(count), "result")
			}
		}
	}
	if activity.Detail == "" && activity.Result == "" && activity.Output == "" && len(activity.Rows) == 0 {
		return nil
	}
	return &activity
}

func activityDetail(arguments json.RawMessage) string {
	var value map[string]any
	if json.Unmarshal(arguments, &value) != nil {
		return ""
	}
	for _, key := range activityDetailKeys {
		text, ok := value[key].(string)
		if !ok || strings.TrimSpace(text) == "" {
			continue
		}
		if key == "url" {
			return activityHost(text)
		}
		return clipActivity(strings.Join(strings.Fields(text), " "), maxActivityText)
	}
	return ""
}

func activityRow(entry any) (aiToolActivityRow, bool) {
	value, ok := entry.(map[string]any)
	if !ok {
		return aiToolActivityRow{}, false
	}
	text := func(keys ...string) string {
		for _, key := range keys {
			if s, ok := value[key].(string); ok && strings.TrimSpace(s) != "" {
				return strings.TrimSpace(s)
			}
		}
		return ""
	}
	address := text("url", "href", "link")
	title := text("title", "name", "label")
	if title == "" {
		title = activityHost(address)
	}
	if title == "" {
		return aiToolActivityRow{}, false
	}
	meta := activityHost(address)
	if meta == title {
		meta = ""
	}
	if meta == "" {
		meta = text("kind", "type", "source")
	}
	return aiToolActivityRow{Title: clipActivity(title, maxActivityText), Meta: clipActivity(meta, 40)}, true
}

// activityHost keeps only a link's site, so query strings never show.
func activityHost(address string) string {
	parsed, err := url.Parse(strings.TrimSpace(address))
	if err != nil || parsed.Host == "" {
		return ""
	}
	return strings.TrimPrefix(parsed.Hostname(), "www.")
}

func countLabel(count int, noun string) string {
	if count == 1 {
		return "1 " + noun
	}
	return fmt.Sprintf("%d %ss", count, noun)
}

func clipActivity(text string, limit int) string {
	runes := []rune(text)
	if len(runes) <= limit {
		return text
	}
	return strings.TrimSpace(string(runes[:limit-1])) + "…"
}
