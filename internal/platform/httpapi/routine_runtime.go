package api

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"time"

	cap "github.com/kannachi323/misty/server/internal/capabilities"
	db "github.com/kannachi323/misty/server/internal/platform/postgres"
)

type routineStepReport struct {
	StepID string `json:"stepId"`
	State  string `json:"state"`
	CallID string `json:"callId,omitempty"`
}
type routineReport struct {
	State string              `json:"state"`
	Steps []routineStepReport `json:"steps"`
}
type routineNextCall struct {
	Binding db.RoutineCallBinding
	Input   json.RawMessage
	Wait    bool
	Agent   *db.RoutineAgentBinding
}

func (s *SpacesService) prepareRoutineRuntime(ctx context.Context, record *db.AIInvocationRecord) (*preparedAIInvocationRuntime, error) {
	run, err := s.database.RoutineRun(ctx, record.UserID, record.ID)
	if err != nil {
		return nil, err
	}
	if run.CancelRequested || run.Outcome != "" || !record.ExpiresAt.After(time.Now()) {
		return nil, db.ErrSpaceConflict
	}
	if _, _, err := cap.ParseRoutineDefinition(run.Execution.Definition); err != nil {
		return nil, err
	}
	registrations, err := s.aiSDKRegistrations(ctx, record)
	if err != nil {
		return nil, err
	}
	available := map[string]bool{}
	for _, registration := range registrations {
		available[registration.Descriptor.Name] = true
	}
	names := []string{}
	for _, binding := range run.Execution.Bindings {
		if !available[binding.ToolName] {
			return nil, db.ErrSDKProviderUnavailable
		}
		names = append(names, binding.ToolName)
	}
	for _, binding := range run.Execution.AgentBindings {
		for _, tool := range binding.Tools {
			if !available[tool.ToolName] {
				return nil, db.ErrSDKProviderUnavailable
			}
			names = append(names, tool.ToolName)
		}
	}
	return &preparedAIInvocationRuntime{routineExecution: &run.Execution, spaceID: record.SpaceID, spaceKind: "routine", allowedTools: uniqueAgentToolNames(names), prompt: "Execute the admitted routine version."}, nil
}

// Agent checkpoints are protected model outputs, not effect receipts. Merge them
// only into this read-only evidence view; all external effects keep one journal.
func (s *SpacesService) routineReceipts(ctx context.Context, record *db.AIInvocationRecord) (map[string]db.RoutineEffectReceipt, error) {
	receipts, err := s.database.RoutineEffectReceipts(ctx, record.UserID, record.ID)
	if err != nil {
		return nil, err
	}
	checkpoints, err := s.database.RoutineAgentCheckpoints(ctx, record.UserID, record.ID)
	if err != nil {
		return nil, err
	}
	for _, checkpoint := range checkpoints {
		receipts["routine-agent:"+checkpoint.CallNamespace] = db.RoutineEffectReceipt{ToolName: "routine.agent", State: checkpoint.State, Ciphertext: checkpoint.Ciphertext}
	}
	waits, err := s.database.RoutineWaits(ctx, record.UserID, record.ID)
	if err != nil {
		return nil, err
	}
	for _, wait := range waits {
		receipts["routine-wait:"+wait.WaitID] = db.RoutineEffectReceipt{ToolName: "routine.wait", State: wait.State}
	}
	return receipts, nil
}
func (s *SpacesService) routineProgress(ctx context.Context, record *db.AIInvocationRecord, run *db.RoutineRunRecord) (routineReport, *routineNextCall, error) {
	receipts, err := s.routineReceipts(ctx, record)
	if err != nil {
		return routineReport{}, nil, err
	}
	return evaluateRoutineEvidence(record, run, receipts, s.restoreAgentEffectResult)
}

type routineEvidencePlan struct {
	Report  routineReport
	Next    *routineNextCall
	Replays map[string]routineNextCall
}

func evaluateRoutineEvidence(record *db.AIInvocationRecord, run *db.RoutineRunRecord, receipts map[string]db.RoutineEffectReceipt, restore func(string, []byte) (json.RawMessage, error)) (routineReport, *routineNextCall, error) {
	plan, err := evaluateRoutinePlan(record, run, receipts, restore)
	return plan.Report, plan.Next, err
}
func evaluateRoutinePlan(record *db.AIInvocationRecord, run *db.RoutineRunRecord, receipts map[string]db.RoutineEffectReceipt, restore func(string, []byte) (json.RawMessage, error)) (routineEvidencePlan, error) {
	plan := routineEvidencePlan{Report: routineReport{State: "completed", Steps: []routineStepReport{}}, Replays: map[string]routineNextCall{}}
	definition, _, err := cap.ParseRoutineDefinition(run.Execution.Definition)
	if err != nil {
		return plan, err
	}
	bindings := map[string]db.RoutineCallBinding{}
	agents := map[string]db.RoutineAgentBinding{}
	for _, binding := range run.Execution.Bindings {
		bindings[binding.StepID] = binding
	}
	for _, binding := range run.Execution.AgentBindings {
		agents[binding.StepID] = binding
	}
	values := map[string]json.RawMessage{}
	stopped, completed := false, false
	for _, step := range definition.Steps {
		binding, exists := bindings[step.ID]
		expression := step.Input
		timed := step.Kind == "wait"
		var agent *db.RoutineAgentBinding
		if step.Kind == "agent" {
			value, found := agents[step.ID]
			exists = found
			agent = &value
			expression = step.Prompt
			binding = db.RoutineCallBinding{StepID: step.ID, CallID: value.CallNamespace, ToolName: "routine.agent"}
		} else if timed {
			exists = true
			expression = step.Until
			binding = db.RoutineCallBinding{StepID: step.ID, CallID: cap.RoutineWaitID(record.UserID, record.ID, step.ID), ToolName: "routine.wait"}
		} else if step.Kind != "capability" {
			return plan, db.ErrSpaceInvalid
		}
		if !exists {
			return plan, db.ErrSpaceInvalid
		}
		item := routineStepReport{StepID: step.ID, State: "not_run", CallID: binding.CallID}
		if stopped {
			plan.Report.Steps = append(plan.Report.Steps, item)
			continue
		}
		allowed, conditionErr := cap.RoutineEvaluateCondition(step.When, run.Execution.Trigger, values)
		if conditionErr != nil {
			item.State = "failed"
			plan.Report.State = "failed"
			stopped = true
		} else if !allowed {
			item.State = "skipped"
		} else {
			input, inputErr := cap.RoutineResolveValue(expression, run.Execution.Trigger, values)
			if inputErr != nil {
				item.State = "failed"
				plan.Report.State = "failed"
				stopped = true
			} else {
				_, effectID := cap.AgentSDKIdentities(record.UserID, record.ID, binding.CallID)
				key, aad := "sdk-agent-effect:"+effectID, effectID+":outcome"
				if agent != nil {
					key = "routine-agent:" + agent.CallNamespace
					aad = key + ":outcome"
				}
				if timed {
					key = "routine-wait:" + binding.CallID
				}
				receipt, found := receipts[key]
				call := routineNextCall{Binding: binding, Input: input, Agent: agent, Wait: timed}
				switch {
				case !found || agent != nil && (receipt.State == "pending" || receipt.State == "running") || timed && (receipt.State == "pending" || receipt.State == "waiting"):
					plan.Next = &call
					plan.Report.State = "failed"
					stopped = true
				case receipt.ToolName != binding.ToolName:
					return plan, db.ErrSpaceConflict
				case receipt.State == "started" || receipt.State == "unknown" || receipt.State == "uncertain":
					item.State = "uncertain"
					plan.Report.State = "uncertain"
					stopped = true
				case agent != nil && receipt.State == "partial":
					item.State = "partial"
					plan.Report.State = "partial"
					stopped = true
				case receipt.State != "completed":
					item.State = "failed"
					plan.Report.State = "failed"
					stopped = true
				default:
					if timed {
						item.State = "completed"
						values[step.ID] = json.RawMessage(`{"resumed":true}`)
						completed = true
						plan.Replays[binding.CallID] = call
						break
					}
					raw, err := restore(aad, receipt.Ciphertext)
					if err != nil {
						return plan, err
					}
					var outcome cap.BackendOutcome
					if json.Unmarshal(raw, &outcome) != nil || outcome.Status != "success" {
						return plan, db.ErrAgentToolboxActionUnknown
					}
					item.State = "completed"
					values[step.ID] = outcome.Result
					completed = true
					plan.Replays[binding.CallID] = call
					if outcome.Partial {
						item.State = "partial"
						plan.Report.State = "partial"
						if agent != nil || !step.AllowPartial {
							stopped = true
						}
					}
				}
			}
		}
		plan.Report.Steps = append(plan.Report.Steps, item)
	}
	if plan.Report.State == "failed" && completed {
		plan.Report.State = "partial"
	}
	return plan, nil
}
func (s *SpacesService) authorizeRoutineCall(ctx context.Context, record *db.AIInvocationRecord, call agentRuntimeToolCall) error {
	run, err := s.database.RoutineRun(ctx, record.UserID, record.ID)
	if err != nil {
		return err
	}
	if run.CancelRequested || run.Outcome != "" || !record.ExpiresAt.After(time.Now()) {
		return db.ErrSpaceConflict
	}
	receipts, err := s.routineReceipts(ctx, record)
	if err != nil {
		return err
	}
	plan, err := evaluateRoutinePlan(record, run, receipts, s.restoreAgentEffectResult)
	if err != nil {
		return err
	}
	if namespace, ok := cap.RoutineAgentCallNamespace(call.CallID); ok {
		// A previous agent step may only replay a call already admitted with the
		// exact tool and input. It cannot spend a new effect ID after completion.
		for _, binding := range run.Execution.AgentBindings {
			if namespace != binding.CallNamespace {
				continue
			}
			allowed := false
			for _, tool := range binding.Tools {
				if tool.ToolName == call.Name {
					allowed = true
				}
			}
			if !allowed {
				return db.ErrAppRuntimeForbidden
			}
			active := plan.Next != nil && plan.Next.Agent != nil && plan.Next.Binding.CallID == namespace
			_, replay := plan.Replays[namespace]
			if !active && !replay {
				return db.ErrSpaceConflict
			}
			return s.database.AdmitRoutineAgentCall(ctx, record.UserID, record.ID, record.RuntimeRunID, binding.StepID, call.CallID, call.Name, call.Arguments, active)
		}
		return db.ErrAppRuntimeForbidden
	}
	expected, exists := plan.Replays[call.CallID]
	if plan.Next != nil && !plan.Next.Wait && plan.Next.Agent == nil && plan.Next.Binding.CallID == call.CallID {
		expected = *plan.Next
		exists = true
	}
	if exists && !expected.Wait && expected.Agent == nil && expected.Binding.ToolName == call.Name && cap.EqualJSON(expected.Input, call.Arguments) {
		return nil
	}
	return db.ErrAppRuntimeForbidden
}
func (s *SpacesService) completeRoutine(ctx context.Context, record *db.AIInvocationRecord, cancelled bool) error {
	run, err := s.database.RoutineRunForRecovery(ctx, record.UserID, record.ID)
	if err != nil {
		return err
	}
	if run.Outcome != "" {
		return nil
	}
	if err := s.recoverRoutineAgents(ctx, record, run); err != nil {
		return err
	}
	report, _, err := s.routineProgress(ctx, record, run)
	if err != nil {
		return err
	}
	waits, err := s.database.RoutineWaits(ctx, record.UserID, record.ID)
	if err != nil {
		return err
	}
	expired := false
	for _, wait := range waits {
		expired = expired || wait.State == "expired"
	}
	if !expired && (cancelled || run.CancelRequested) && report.State == "failed" {
		report.State = "cancelled"
	}
	raw, err := json.Marshal(report)
	if err != nil {
		return err
	}
	return s.database.PublishRoutineCompletion(ctx, record.UserID, record.ID, report.State, raw)
}

var errRoutineExecutionUnsupported = errors.New("routine execution requires the negotiated MCP runtime")

func (s *SpacesService) recordRoutineStepEvent(ctx context.Context, record *db.AIInvocationRecord, runtimeID, nodeID, state string) error {
	// Common harness discovery checkpoints precede the routine-specific steps.
	if !strings.HasPrefix(nodeID, "routine:") {
		if nodeID != "mcp:catalog" || state != "completed" {
			return db.ErrSpaceInvalid
		}
		payload := json.RawMessage(`{"type":"routine.runtime","phase":"catalog_loaded"}`)
		_, err := s.database.CommitAIInvocationEvent(ctx, record.UserID, record.ID, runtimeID+":mcp:catalog:completed", "routine.runtime", payload, "")
		return err
	}
	if state != "running" && state != "completed" && state != "failed" {
		return db.ErrSpaceInvalid
	}
	run, err := s.database.RoutineRun(ctx, record.UserID, record.ID)
	if err != nil {
		return err
	}
	report, next, err := s.routineProgress(ctx, record, run)
	if err != nil {
		return err
	}
	stepID := strings.TrimPrefix(nodeID, "routine:")
	observed := ""
	for _, step := range report.Steps {
		if step.StepID == stepID {
			observed = step.State
			break
		}
	}
	if observed == "" {
		return db.ErrSpaceInvalid
	}
	if state == "running" && next != nil && next.Binding.StepID == stepID {
		observed = "running"
	}
	payload, _ := json.Marshal(map[string]any{"type": "routine.step", "step_id": stepID, "step_state": observed, "routine_id": run.Execution.RoutineID, "routine_version": run.Execution.Version})
	_, err = s.database.CommitAIInvocationEvent(ctx, record.UserID, record.ID, runtimeID+":"+nodeID+":"+state, "routine.step", payload, "")
	return err
}
