package workflow

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"sync"
)

func (engine Engine) executeForEach(ctx context.Context, parent ExecutionRequest, invocation Invocation) (json.RawMessage, bool, error) {
	var config TestingForEachConfig
	if json.Unmarshal(invocation.Config, &config) != nil {
		return nil, false, ErrInvalidDefinition
	}
	child, err := engine.childDefinition(config)
	if err != nil {
		return nil, false, err
	}
	items, err := workflowItems(invocation.Input)
	if err != nil {
		return nil, false, err
	}
	if config.MaximumItems == 0 {
		config.MaximumItems = 1000
	}
	if config.MaximumItems < 1 || config.MaximumItems > 10000 || len(items) > config.MaximumItems {
		return nil, false, ErrInvalidDefinition
	}
	if config.Sequential {
		config.Concurrency = 1
	}
	if config.Concurrency == 0 {
		config.Concurrency = 4
	}
	if config.Concurrency < 1 || config.Concurrency > 32 {
		return nil, false, ErrInvalidDefinition
	}
	if config.ErrorMode == "" {
		config.ErrorMode = "collect"
	}
	if config.ErrorMode != "collect" && config.ErrorMode != "continue" && config.ErrorMode != "fail_fast" {
		return nil, false, ErrInvalidDefinition
	}
	type itemResult struct {
		Index   int                        `json:"index"`
		Input   json.RawMessage            `json:"input"`
		Outputs map[string]json.RawMessage `json:"outputs,omitempty"`
		Errors  map[string]string          `json:"errors,omitempty"`
		Error   string                     `json:"error,omitempty"`
	}
	results := make([]itemResult, len(items))
	workCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	semaphore := make(chan struct{}, config.Concurrency)
	var wait sync.WaitGroup
	for index, item := range items {
		if workCtx.Err() != nil {
			break
		}
		wait.Add(1)
		go func(index int, item json.RawMessage) {
			defer wait.Done()
			select {
			case semaphore <- struct{}{}:
			case <-workCtx.Done():
				return
			}
			defer func() { <-semaphore }()
			result, executeErr := engine.Execute(workCtx, child, ExecutionRequest{RunID: parent.RunID, UserID: parent.UserID, SpaceID: parent.SpaceID, Input: item, NodePrefix: fmt.Sprintf("%s.item_%06d.", invocation.NodeID, index), Completed: parent.Completed})
			if engine.ItemCheckpoint != nil {
				if checkpointErr := engine.ItemCheckpoint(context.WithoutCancel(workCtx), invocation.NodeID, item, result, executeErr); checkpointErr != nil && executeErr == nil {
					executeErr = checkpointErr
				}
			}
			results[index] = itemResult{Index: index, Input: item, Outputs: result.Outputs, Errors: result.Errors}
			if executeErr != nil {
				results[index].Error = executeErr.Error()
			}
			if (executeErr != nil || result.State != RunCompleted) && config.ErrorMode == "fail_fast" {
				cancel()
			}
		}(index, item)
	}
	wait.Wait()
	partial := false
	errorsByItem := map[string]string{}
	for index := range results {
		if results[index].Error != "" || len(results[index].Errors) > 0 {
			partial = true
			errorsByItem[fmt.Sprintf("%d", index)] = results[index].Error
			if errorsByItem[fmt.Sprintf("%d", index)] == "" {
				errorsByItem[fmt.Sprintf("%d", index)] = "completed_with_errors"
			}
		}
	}
	output, _ := json.Marshal(map[string]any{"items": results, "errors": errorsByItem, "partial": partial})
	if partial && config.ErrorMode == "fail_fast" {
		return output, true, errors.New("for_each child failed")
	}
	return output, partial, nil
}

func (engine Engine) childDefinition(config TestingForEachConfig) (Definition, error) {
	if config.ChildGraph != nil && config.WorkflowVersionID != "" {
		return Definition{}, ErrInvalidDefinition
	}
	if config.ChildGraph != nil {
		return *config.ChildGraph, nil
	}
	if config.WorkflowVersionID == "" || engine.Resolver == nil {
		return Definition{}, ErrInvalidDefinition
	}
	_, _, child, ok := engine.Resolver.ResolveWorkflowVersion(config.WorkflowVersionID)
	if !ok {
		return Definition{}, ErrInvalidDefinition
	}
	return child, nil
}

func workflowItems(raw json.RawMessage) ([]json.RawMessage, error) {
	var value any
	if json.Unmarshal(raw, &value) != nil {
		return nil, ErrOutputInvalid
	}
	var find func(any) []any
	find = func(current any) []any {
		switch item := current.(type) {
		case []any:
			return item
		case map[string]any:
			for _, key := range []string{"items", "events", "value"} {
				if found := find(item[key]); found != nil {
					return found
				}
			}
		}
		return nil
	}
	values := find(value)
	if values == nil {
		return nil, ErrOutputInvalid
	}
	out := make([]json.RawMessage, 0, len(values))
	for _, item := range values {
		rawItem, err := json.Marshal(item)
		if err != nil {
			return nil, ErrOutputInvalid
		}
		out = append(out, rawItem)
	}
	return out, nil
}

func marshalExecutionResult(result ExecutionResult) json.RawMessage {
	raw, _ := json.Marshal(map[string]any{"state": result.State, "outputs": result.Outputs, "errors": result.Errors})
	return raw
}

func (engine Engine) checkpoint(ctx context.Context, event StepEvent) error {
	if engine.Checkpoint == nil {
		return nil
	}
	return engine.Checkpoint(ctx, event)
}

func topologicalOrder(definition Definition) []Node {
	byID, indegree, outgoing := map[string]Node{}, map[string]int{}, map[string][]string{}
	for _, node := range definition.Nodes {
		byID[node.ID], indegree[node.ID] = node, 0
	}
	for _, edge := range definition.Edges {
		indegree[edge.Target]++
		outgoing[edge.Source] = append(outgoing[edge.Source], edge.Target)
	}
	queue := []string{}
	for id, degree := range indegree {
		if degree == 0 {
			queue = append(queue, id)
		}
	}
	sort.Strings(queue)
	out := make([]Node, 0, len(byID))
	for len(queue) > 0 {
		id := queue[0]
		queue = queue[1:]
		out = append(out, byID[id])
		for _, target := range outgoing[id] {
			indegree[target]--
			if indegree[target] == 0 {
				queue = append(queue, target)
				sort.Strings(queue)
			}
		}
	}
	return out
}
