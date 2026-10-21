package api

import (
	"encoding/json"
	"net/url"
	"regexp"
	"strings"
)

// browser_extract answers through a model, and a model copies long links badly: on Depop it
// returned a seller's listing links with one character changed in each, and the agent spent
// minutes fetching pages that don't exist. Every link in an extraction must be one the page
// links to. A near copy of a page link (same site, a few characters off) is put right; any
// other is removed, and the result says which.

var (
	markdownLink = regexp.MustCompile(`\]\(([^)\s]+)`)
	bareLink     = regexp.MustCompile(`https?://[^\s)\]"'<>]+`)
)

// pageLinks lists the absolute links a page's markdown holds, resolved against its URL.
func pageLinks(markdown, base string) map[string]bool {
	baseURL, _ := url.Parse(base)
	links := map[string]bool{}
	add := func(raw string) {
		parsed, err := url.Parse(strings.TrimRight(raw, ".,;"))
		if err != nil {
			return
		}
		if baseURL != nil {
			parsed = baseURL.ResolveReference(parsed)
		}
		if parsed.Scheme == "http" || parsed.Scheme == "https" {
			parsed.Fragment = ""
			links[parsed.String()] = true
		}
	}
	for _, match := range markdownLink.FindAllStringSubmatch(markdown, -1) {
		add(match[1])
	}
	for _, match := range bareLink.FindAllString(markdown, -1) {
		add(match)
	}
	if baseURL != nil {
		links[baseURL.String()] = true
	}
	return links
}

// verifyExtractedLinks checks each link-like string in `data` against the page's links, in
// place. It returns how many it corrected and the ones it removed.
func verifyExtractedLinks(data json.RawMessage, markdown, base string) (json.RawMessage, int, []string) {
	var value any
	if json.Unmarshal(data, &value) != nil {
		return data, 0, nil
	}
	links := pageLinks(markdown, base)
	baseURL, _ := url.Parse(base)
	corrected := 0
	var removed []string
	var walk func(any) any
	walk = func(node any) any {
		switch typed := node.(type) {
		case map[string]any:
			for key, child := range typed {
				typed[key] = walk(child)
			}
			return typed
		case []any:
			for index, child := range typed {
				typed[index] = walk(child)
			}
			return typed
		case string:
			candidate, ok := linkLike(typed, baseURL)
			if !ok || links[candidate] {
				if ok && candidate != typed {
					return candidate
				}
				return typed
			}
			if near := nearestLink(candidate, links); near != "" {
				corrected++
				return near
			}
			removed = append(removed, typed)
			return ""
		default:
			return node
		}
	}
	value = walk(value)
	out, err := json.Marshal(value)
	if err != nil {
		return data, 0, nil
	}
	return out, corrected, removed
}

// linkLike resolves a string that is a link (absolute, or a path on the page's site).
func linkLike(text string, base *url.URL) (string, bool) {
	text = strings.TrimSpace(text)
	if strings.ContainsAny(text, " \n\t") || len(text) > 2048 {
		return "", false
	}
	if !strings.HasPrefix(text, "http://") && !strings.HasPrefix(text, "https://") && !(strings.HasPrefix(text, "/") && !strings.HasPrefix(text, "//")) {
		return "", false
	}
	parsed, err := url.Parse(text)
	if err != nil {
		return "", false
	}
	if base != nil {
		parsed = base.ResolveReference(parsed)
	}
	if parsed.Host == "" {
		return "", false
	}
	parsed.Fragment = ""
	return parsed.String(), true
}

// nearestLink is the page link on the same site within a few edits of `link`, or "".
func nearestLink(link string, links map[string]bool) string {
	target, err := url.Parse(link)
	if err != nil {
		return ""
	}
	limit := 3 + len(link)/40
	best, bestDistance := "", limit+1
	for candidate := range links {
		parsed, err := url.Parse(candidate)
		if err != nil || parsed.Host != target.Host {
			continue
		}
		if gap := len(candidate) - len(link); gap > limit || -gap > limit {
			continue
		}
		if distance := editDistance(link, candidate, bestDistance); distance < bestDistance {
			best, bestDistance = candidate, distance
		}
	}
	if bestDistance > limit {
		return ""
	}
	return best
}

// editDistance is the Levenshtein distance, giving up once it passes `cap`.
func editDistance(a, b string, cap int) int {
	previous := make([]int, len(b)+1)
	current := make([]int, len(b)+1)
	for j := range previous {
		previous[j] = j
	}
	for i := 1; i <= len(a); i++ {
		current[0] = i
		lowest := current[0]
		for j := 1; j <= len(b); j++ {
			cost := 1
			if a[i-1] == b[j-1] {
				cost = 0
			}
			current[j] = min(previous[j]+1, current[j-1]+1, previous[j-1]+cost)
			lowest = min(lowest, current[j])
		}
		if lowest > cap {
			return cap + 1
		}
		previous, current = current, previous
	}
	return previous[len(b)]
}
