package api

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"strings"
)

// Serper sells Google's result tabs as JSON, one endpoint per tab. Each kind
// below is one request.
var webSearchBase = "https://google.serper.dev"

// credits is what Serper charges for one request of the kind (measured
// 2026-10-16); billing uses the count each response reports when it has one.
var webSearchKinds = map[string]struct {
	path, list string
	credits    int64
}{
	"web":      {"/search", "organic", 1},
	"news":     {"/news", "news", 1},
	"videos":   {"/videos", "videos", 1},
	"images":   {"/images", "images", 1},
	"places":   {"/places", "places", 1},
	"shopping": {"/shopping", "shopping", 2},
	"scholar":  {"/scholar", "organic", 1},
	"patents":  {"/patents", "organic", 1},
	"maps":     {"/maps", "places", 3},
	// Autocomplete returns what people type, not pages: suggestions without links.
	"autocomplete": {"/autocomplete", "suggestions", 1},
}

var serperRecency = map[string]string{"day": "qdr:d", "week": "qdr:w", "month": "qdr:m", "year": "qdr:y"}

// webSearchHit is one result; fields past the snippet appear only for the kinds that have them.
type webSearchHit struct {
	Title       string  `json:"title"`
	URL         string  `json:"url,omitempty"`
	Snippet     string  `json:"snippet,omitempty"`
	Published   string  `json:"published,omitempty"`
	Source      string  `json:"source,omitempty"`
	Channel     string  `json:"channel,omitempty"`
	Duration    string  `json:"duration,omitempty"`
	Price       string  `json:"price,omitempty"`
	Rating      float64 `json:"rating,omitempty"`
	Reviews     int     `json:"reviews,omitempty"`
	Address     string  `json:"address,omitempty"`
	Phone       string  `json:"phone,omitempty"`
	Category    string  `json:"category,omitempty"`
	Image       string  `json:"image,omitempty"`
	Publication string  `json:"publication,omitempty"`
	CitedBy     int     `json:"cited_by,omitempty"`
}

type webSearchAnswer struct {
	// Credits is what the request cost at Serper.
	Credits int64          `json:"-"`
	Results []webSearchHit `json:"results"`
	// Answer is Google's direct answer or knowledge panel text, when there is one.
	Answer string `json:"answer,omitempty"`
}

// serperQuery is the request body: site: filters join the query, recency is tbs,
// and a map search can center on coordinates ("@lat,lng,zoomz").
func serperQuery(query string, domains []string, recency, location, near string, limit int) map[string]any {
	sites := make([]string, 0, len(domains))
	for _, domain := range domains {
		if domain = strings.TrimSpace(domain); domain != "" {
			sites = append(sites, "site:"+domain)
		}
	}
	if len(sites) > 0 {
		query += " (" + strings.Join(sites, " OR ") + ")"
	}
	body := map[string]any{"q": query, "num": limit}
	if tbs := serperRecency[recency]; tbs != "" {
		body["tbs"] = tbs
	}
	if location = strings.TrimSpace(location); location != "" {
		body["location"] = location
	}
	if near = strings.TrimSpace(near); near != "" {
		body["ll"] = near
	}
	return body
}

func field(item map[string]any, names ...string) string {
	for _, name := range names {
		switch value := item[name].(type) {
		case string:
			if value = strings.TrimSpace(value); value != "" {
				return value
			}
		case float64:
			return fmt.Sprint(value)
		}
	}
	return ""
}

func number(item map[string]any, name string) float64 {
	value, _ := item[name].(float64)
	return value
}

// serperHit reads one result of any kind; a place without a website links to its map entry.
func serperHit(item map[string]any) (webSearchHit, bool) {
	link := field(item, "link", "website")
	if link == "" && field(item, "cid") != "" {
		link = "https://maps.google.com/?cid=" + url.QueryEscape(field(item, "cid"))
	}
	if !strings.HasPrefix(link, "https://") && !strings.HasPrefix(link, "http://") {
		return webSearchHit{}, false
	}
	hit := webSearchHit{
		Title: field(item, "title"), URL: link, Snippet: field(item, "snippet", "description"),
		Published: field(item, "date", "year", "publicationDate", "priorityDate"),
		Source:    field(item, "source"), Channel: field(item, "channel"), Duration: field(item, "duration"), Price: field(item, "price"),
		Rating: number(item, "rating"), Reviews: int(number(item, "ratingCount")), Address: field(item, "address"),
		Phone: field(item, "phoneNumber"), Category: field(item, "category", "type"), Image: field(item, "imageUrl"),
		Publication: field(item, "publicationInfo"), CitedBy: int(number(item, "citedBy")),
	}
	if hit.Title == "" {
		hit.Title = link
	}
	return hit, true
}

// serperAnswer reads one kind's result list plus any direct answer.
func serperAnswer(raw []byte, list string, credits int64, limit int) (webSearchAnswer, error) {
	var body map[string]json.RawMessage
	if err := json.Unmarshal(raw, &body); err != nil {
		return webSearchAnswer{}, err
	}
	var items []map[string]any
	_ = json.Unmarshal(body[list], &items)
	answer := webSearchAnswer{Credits: credits, Results: []webSearchHit{}}
	if reported, ok := body["credits"]; ok {
		_ = json.Unmarshal(reported, &answer.Credits)
	}
	for _, item := range items {
		if suggestion := field(item, "value"); list == "suggestions" && suggestion != "" {
			answer.Results = append(answer.Results, webSearchHit{Title: suggestion})
		} else if hit, ok := serperHit(item); ok {
			answer.Results = append(answer.Results, hit)
		}
		if len(answer.Results) >= limit {
			break
		}
	}
	var direct struct {
		AnswerBox      struct{ Answer, Snippet string } `json:"answerBox"`
		KnowledgeGraph struct{ Description string }     `json:"knowledgeGraph"`
	}
	_ = json.Unmarshal(raw, &direct)
	for _, text := range []string{direct.AnswerBox.Answer, direct.AnswerBox.Snippet, direct.KnowledgeGraph.Description} {
		if text = strings.TrimSpace(text); text != "" {
			answer.Answer = truncateAgentRuntimeText(text, 600)
			break
		}
	}
	return answer, nil
}

var errWebSearchFailed = errors.New("web search failed")

func serperSearch(ctx context.Context, key, kind string, request map[string]any, limit int) (webSearchAnswer, error) {
	endpoint := webSearchKinds[kind]
	payload, _ := json.Marshal(request)
	call, err := http.NewRequestWithContext(ctx, http.MethodPost, webSearchBase+endpoint.path, bytes.NewReader(payload))
	if err != nil {
		return webSearchAnswer{}, err
	}
	call.Header.Set("X-API-KEY", key)
	call.Header.Set("Content-Type", "application/json")
	response, err := http.DefaultClient.Do(call)
	if err != nil {
		return webSearchAnswer{}, errWebSearchFailed
	}
	defer response.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(response.Body, webSearchMaxBody))
	if err != nil || response.StatusCode != http.StatusOK {
		log.Printf("web search: serper %s returned %d", kind, response.StatusCode)
		return webSearchAnswer{}, errWebSearchFailed
	}
	return serperAnswer(raw, endpoint.list, endpoint.credits, limit)
}
