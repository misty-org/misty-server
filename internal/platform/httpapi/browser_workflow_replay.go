package api

import (
	"context"
	"encoding/json"
	"log"
	"strings"

	db "github.com/kannachi323/misty/server/internal/platform/postgres"
)

// Cached workflow replays (docs/plans/agentic-browser.md, Phase 3). A workflow
// run whose every effect was a browser action is saved with each control named
// by locator; the next run of that workflow replays it before calling a model.

const maxReplayCalls = 100

// Tools a replayable run may have used: browser reads, which the replay
// re-takes as needed, and the browser actions it re-runs.
var replayableBrowserTools = map[string]bool{
	"browser.navigate": true, "browser.snapshot": true, "browser.find": true, "browser.wait_for": true,
	"browser.steps": true, "browser.fill_form": true, "browser.skill": true,
	"browser.inspect": true, "browser.visual": true, "browser.downloads.list": true,
}

type replayStep = map[string]any

// buildBrowserReplay turns a successful run's browser jobs into replay calls.
// ok is false when any effect cannot be reproduced exactly: another tool's
// effect, a screen-agent goal, a file upload, data the model extracted, or a
// control without a recorded locator.
func buildBrowserReplay(toolNames []string, jobs []db.BrowserJobRecord) (startURL string, calls []map[string]any, ok bool) {
	for _, name := range toolNames {
		if !replayableBrowserTools[name] {
			return "", nil, false
		}
	}
	for _, job := range jobs {
		var input map[string]any
		var output map[string]any
		if json.Unmarshal(job.Input, &input) != nil || json.Unmarshal(job.Output, &output) != nil {
			return "", nil, false
		}
		switch job.Operation {
		case "browser.snapshot", "browser.find", "browser.inspect", "browser.visual", "browser.downloads.list":
			if startURL == "" && len(calls) == 0 {
				startURL, _ = output["url"].(string)
			}
		case "browser.navigate":
			url, _ := input["url"].(string)
			if url == "" {
				return "", nil, false
			}
			calls = append(calls, map[string]any{"tool": "browser.navigate", "args": map[string]any{"url": url}})
		case "browser.wait_for":
			args := map[string]any{}
			for _, key := range []string{"text", "textGone", "urlIncludes", "timeoutMs"} {
				if value, present := input[key]; present {
					args[key] = value
				}
			}
			calls = append(calls, map[string]any{"tool": "browser.wait_for", "args": args})
		case "browser.steps", "browser.fill_form":
			steps, replayable := replaySteps(job.Operation, input, output)
			if !replayable {
				return "", nil, false
			}
			if len(steps) > 0 {
				args := map[string]any{"steps": steps}
				if input["allowConsequential"] == true {
					args["allowConsequential"] = true
				}
				calls = append(calls, map[string]any{"tool": "browser.steps", "args": args})
			}
		case "browser.skill":
			if output["ok"] != true || mentionsRef(input["args"]) {
				return "", nil, false
			}
			args := map[string]any{"skill": input["skill"], "action": input["action"], "args": input["args"]}
			if input["allowConsequential"] == true {
				args["allowConsequential"] = true
			}
			calls = append(calls, map[string]any{"tool": "browser.skill", "args": args})
		default:
			return "", nil, false
		}
		if len(calls) > maxReplayCalls {
			return "", nil, false
		}
	}
	return startURL, calls, len(calls) > 0
}

// replaySteps rebuilds the steps that ran, each ref replaced by its locator.
func replaySteps(operation string, input, output map[string]any) ([]replayStep, bool) {
	records, _ := output["steps"].([]any)
	var requested []replayStep
	if operation == "browser.steps" {
		list, _ := input["steps"].([]any)
		for _, item := range list {
			step, _ := item.(map[string]any)
			requested = append(requested, step)
		}
	} else {
		fields, _ := input["fields"].([]any)
		for _, item := range fields {
			field, _ := item.(map[string]any)
			step := replayStep{"ref": field["ref"]}
			switch value := field["value"].(type) {
			case string:
				step["action"], step["text"] = "fill", value
			case float64:
				step["action"], step["text"] = "fill", jsonNumber(value)
			case bool:
				step["action"] = map[bool]string{true: "check", false: "uncheck"}[value]
			case []any:
				step["action"], step["values"] = "select", value
			default:
				return nil, false
			}
			requested = append(requested, step)
		}
	}
	out := []replayStep{}
	for _, item := range records {
		record, _ := item.(map[string]any)
		index, _ := record["index"].(float64)
		if record["ok"] != true || index < 1 || int(index) > len(requested) {
			continue
		}
		step := replayStep{}
		for key, value := range requested[int(index)-1] {
			step[key] = value
		}
		if _, hasRef := step["ref"]; hasRef {
			locator, _ := record["locator"].(map[string]any)
			if locator == nil {
				return nil, false
			}
			delete(step, "ref")
			step["locator"] = locator
		}
		out = append(out, step)
	}
	return out, true
}

func jsonNumber(value float64) string {
	encoded, _ := json.Marshal(value)
	return string(encoded)
}

func mentionsRef(value any) bool {
	switch typed := value.(type) {
	case map[string]any:
		for key, item := range typed {
			if key == "ref" || mentionsRef(item) {
				return true
			}
		}
	case []any:
		for _, item := range typed {
			if mentionsRef(item) {
				return true
			}
		}
	}
	return false
}

// recordBrowserWorkflowReplay saves a successful workflow run for replay, or
// forgets an older replay when this run cannot be replayed. Best effort: a
// failure here never changes the run's own result.
func (s *SpacesService) recordBrowserWorkflowReplay(ctx context.Context, record *db.AIInvocationRecord, body aiInvocationInput) {
	if record == nil || strings.TrimSpace(body.MethodVersionID) == "" {
		return
	}
	names, err := s.database.InvocationToolNames(ctx, record.UserID, record.ID)
	if err != nil {
		log.Printf("browser replay: tool names for %s: %v", record.ID, err)
		return
	}
	jobs, err := s.database.CompletedBrowserJobs(ctx, record.UserID, record.ID)
	if err != nil {
		log.Printf("browser replay: jobs for %s: %v", record.ID, err)
		return
	}
	startURL, calls, ok := buildBrowserReplay(names, jobs)
	if !ok {
		// The saved steps no longer describe this workflow's last good run.
		_ = s.database.DeleteBrowserWorkflowReplay(ctx, record.UserID, body.MethodVersionID)
		return
	}
	encoded, err := json.Marshal(calls)
	if err != nil {
		return
	}
	if err := s.database.SaveBrowserWorkflowReplay(ctx, record.UserID, db.BrowserWorkflowReplay{
		MethodVersionID: body.MethodVersionID, StartURL: startURL, Calls: encoded, SourceInvocationID: record.ID,
	}); err != nil {
		log.Printf("browser replay: save for %s: %v", record.ID, err)
	}
}

// browserWorkflowReplayContext hands a workflow run its saved replay and the
// browser it runs in, when the run has a browser that can act by ref.
func (s *SpacesService) browserWorkflowReplayContext(ctx context.Context, record *db.AIInvocationRecord, body aiInvocationInput) map[string]any {
	if record == nil || strings.TrimSpace(body.MethodVersionID) == "" {
		return nil
	}
	replay, err := s.database.BrowserWorkflowReplayFor(ctx, record.UserID, body.MethodVersionID)
	if err != nil || replay == nil {
		return nil
	}
	contexts, err := s.database.AIInvocationContexts(ctx, record.UserID, record.ID)
	if err != nil {
		return nil
	}
	for _, item := range contexts {
		var capabilities []string
		if item.Kind != "browser_tab" || item.State != "attached" || json.Unmarshal(item.Capabilities, &capabilities) != nil ||
			!containsString(capabilities, "browser.steps") || !containsString(capabilities, "browser.snapshot") {
			continue
		}
		return map[string]any{"scope_id": item.OpaqueRef, "start_url": replay.StartURL, "calls": replay.Calls, "recorded_at": replay.RecordedAt}
	}
	return nil
}
