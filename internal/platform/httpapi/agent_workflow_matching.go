package api

import (
	"context"
	"sort"
	"strings"
	"unicode"

	"github.com/kannachi323/misty/server/internal/agenttools"
	"github.com/kannachi323/misty/server/internal/platform/db"
)

// findWorkflowsByMeaning ranks the run's workflows by how close they are in
// meaning to the request, best first.
func findWorkflowsByMeaning(ctx context.Context, database *db.Database, search memorySearchFunc, invocation agenttools.Invocation, methods []db.AgentMethod, request string, limit int) []map[string]any {
	if search == nil || len(methods) == 0 {
		return nil
	}
	space, err := runMemorySpace(ctx, database, invocation)
	if err != nil {
		return nil
	}
	hits, err := search(ctx, invocation.UserID, space, []string{db.MemoryWorkflows}, request, 20)
	if err != nil {
		return nil
	}
	byID := make(map[string]db.AgentMethod, len(methods))
	for _, method := range methods {
		byID[method.ID] = method
	}
	out := []map[string]any{}
	for _, hit := range hits {
		method, ok := byID[hit.RecordID]
		if !ok || len(out) == limit {
			continue
		}
		out = append(out, map[string]any{"id": method.ID, "title": method.Definition.Title,
			"description": method.Definition.Description, "inputs": method.Definition.Inputs})
	}
	return out
}

// matchWorkflows ranks saved workflows by how many of the request's words their
// title, description and instructions share, best first.
func matchWorkflows(methods []db.AgentMethod, request string, limit int) []map[string]any {
	words := func(text string) map[string]bool {
		set := map[string]bool{}
		for _, word := range strings.FieldsFunc(strings.ToLower(text), func(r rune) bool { return !unicode.IsLetter(r) && !unicode.IsNumber(r) }) {
			if len(word) > 2 {
				set[word] = true
			}
		}
		return set
	}
	wanted := words(request)
	type scored struct {
		method db.AgentMethod
		score  int
	}
	ranked := []scored{}
	for _, method := range methods {
		score := 0
		for word := range words(method.Definition.Title + " " + method.Definition.Description + " " + method.Definition.Instructions) {
			if wanted[word] {
				score++
			}
		}
		if score > 0 {
			ranked = append(ranked, scored{method, score})
		}
	}
	sort.SliceStable(ranked, func(a, b int) bool { return ranked[a].score > ranked[b].score })
	out := []map[string]any{}
	for _, item := range ranked {
		if len(out) == limit {
			break
		}
		out = append(out, map[string]any{"id": item.method.ID, "title": item.method.Definition.Title,
			"description": item.method.Definition.Description, "inputs": item.method.Definition.Inputs, "shared_words": item.score})
	}
	return out
}
