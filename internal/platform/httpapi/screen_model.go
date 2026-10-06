package api

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strconv"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/kannachi323/misty/server/internal/modelruntime"
)

// The desktop runs Midscene's screen loop for a browser.act job and sends its
// model calls here. Misty admits each call against the run's model-turn budget,
// meters it to the run and makes it through the agent runtime, so the desktop
// never holds a model key.
const (
	screenActTool        = "browser.act"
	screenModelBodyLimit = 12 << 20
	screenModelMaxOutput = 6000
	screenModelMaxCalls  = 40
)

// ScreenModel answers one OpenAI-style chat completion for a live act job.
func (s *SpacesService) ScreenModel() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		user, ok := s.appsUser(w, r)
		if !ok {
			return
		}
		w.Header().Set("Cache-Control", "no-store")
		job, err := s.database.WorkflowDeviceNodeJob(r.Context(), user, chi.URLParam(r, "jobID"))
		if err != nil || job.Operation != screenActTool || (job.State != "leased" && job.State != "executing") || time.Now().After(job.DeadlineAt) {
			writeJSON(w, http.StatusForbidden, map[string]string{"code": "screen_act_inactive", "message": "This screen task is no longer running."})
			return
		}
		call, err := strconv.Atoi(r.URL.Query().Get("call"))
		if err != nil || call < 0 || call >= screenModelMaxCalls {
			writeJSON(w, http.StatusBadRequest, map[string]string{"code": "screen_model_call_limit", "message": "This screen task used all of its model calls."})
			return
		}
		var body struct {
			Messages json.RawMessage `json:"messages"`
		}
		raw, err := io.ReadAll(io.LimitReader(r.Body, screenModelBodyLimit+1))
		if err != nil || len(raw) > screenModelBodyLimit || json.Unmarshal(raw, &body) != nil || len(body.Messages) < 2 || body.Messages[0] != '[' {
			writeJSON(w, http.StatusBadRequest, map[string]string{"code": "screen_model_invalid", "message": "Invalid screen model request."})
			return
		}
		system, messages, err := screenModelMessages(body.Messages)
		if err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"code": "screen_model_invalid", "message": "Invalid screen model request."})
			return
		}
		record, err := s.database.AIInvocationByID(r.Context(), user, job.RunID)
		if err != nil {
			writeSpaceError(w, err)
			return
		}
		node := "model:screen:" + job.ID + ":" + strconv.Itoa(call)
		if err := s.meterAIInvocationRuntimeModel(r.Context(), record, node, "running", TestingMustAPIRawJSON(map[string]any{"input_bytes": len(raw)})); err != nil {
			writeJSON(w, http.StatusPaymentRequired, map[string]string{"code": "screen_model_budget", "message": "The task's model allowance is used up."})
			return
		}
		route, model, err := s.screenModelRoute(r.Context(), record)
		if err != nil {
			writeAIProviderError(w, err)
			return
		}
		callContext, cancel := context.WithTimeout(r.Context(), 2*time.Minute)
		result, err := s.models.Text(callContext, modelruntime.TextRequest{
			Route: route, Model: model, System: system, Messages: messages, MaxOutputTokens: screenModelMaxOutput,
		})
		cancel()
		completion := map[string]any{"usage": map[string]any{"inputTokens": result.Usage.InputTokens, "outputTokens": result.Usage.OutputTokens}}
		if err != nil {
			completion = map[string]any{}
		}
		_ = s.meterAIInvocationRuntimeModel(context.WithoutCancel(r.Context()), record, node, "completed", TestingMustAPIRawJSON(completion))
		if err != nil {
			writeJSON(w, http.StatusBadGateway, map[string]string{"code": "screen_model_failed", "message": "The screen model could not answer. Try again."})
			return
		}
		// The planner reads an OpenAI chat completion.
		writeJSON(w, http.StatusOK, map[string]any{
			"object": "chat.completion", "model": model,
			"choices": []map[string]any{{"index": 0, "message": map[string]any{"role": "assistant", "content": result.Text}, "finish_reason": "stop"}},
			"usage": map[string]any{"prompt_tokens": result.Usage.InputTokens, "completion_tokens": result.Usage.OutputTokens,
				"total_tokens": result.Usage.InputTokens + result.Usage.OutputTokens},
		})
	}
}
