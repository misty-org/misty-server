package workflow

import (
	"context"
	"encoding/json"
)

// CoreRegistry is the canonical editor/runtime catalog. Runtime services
// replace the unavailable handler by registering provider-specific adapters in
// their own registry; this catalog is also used for publication validation.
func CoreRegistry() *Registry {
	registry := NewRegistry()
	register := func(kind, capability string, risk Risk, location ExecutionLocation, idempotent, reconcile bool) {
		inputSchema, outputSchema := coreNodeSchemas(kind)
		_ = registry.Register(NodeDescriptor{
			Kind: kind, Version: 1, Capability: capability, Risk: risk, Location: location,
			InputSchema: inputSchema, OutputSchema: outputSchema,
			ToolSchema: JSONSchema{"type": "object"}, TriggerSchema: JSONSchema{"type": "object"}, EditorMetadata: map[string]any{"group": "core", "label": kind},
			Idempotent: idempotent, SupportsReconcile: reconcile,
			HealthCheck: func(context.Context) error { return nil }, Cancel: func(context.Context, Invocation) error { return nil },
			Execute: func(context.Context, Invocation) (json.RawMessage, error) { return nil, ErrProviderMissing },
		})
	}
	for _, kind := range []string{"manual_trigger", "chat_trigger", "cron_trigger", "file_changes", "library_changes", "message_trigger", "connector_trigger", "task_change_trigger"} {
		register(kind, "triggers.read", RiskRead, LocationCloud, true, false)
	}
	register("changed_files", "files.read", RiskRead, LocationDevice, true, false)
	register("source_query", "content.read", RiskRead, LocationEither, true, false)
	register("read_content", "content.read", RiskRead, LocationEither, true, false)
	register("read_metadata", "content.read", RiskRead, LocationEither, true, false)
	register("task_query", "tasks.read", RiskRead, LocationCloud, true, false)
	register("calendar_query", "calendar.read", RiskRead, LocationCloud, true, false)
	for _, kind := range []string{"transform", "for_each", "condition", "switch", "join", "debounce", "delay", "call_workflow"} {
		register(kind, "workflow.control", RiskRead, LocationCloud, true, false)
	}
	register("agent_task", "agent.reason", RiskRead, LocationCloud, true, false)
	register("create_document", "files.write", RiskWrite, LocationEither, true, false)
	register("write_library_artifact", "library.write", RiskWrite, LocationCloud, true, false)
	register("notify_private", "notifications.write", RiskWrite, LocationCloud, true, false)
	register("post_reply", "messages.write", RiskWrite, LocationCloud, true, false)
	register("update_metadata", "library.write", RiskWrite, LocationCloud, true, false)
	register("memory_write", "memory.write", RiskWrite, LocationCloud, true, false)
	register("create_task", "tasks.write", RiskWrite, LocationCloud, true, false)
	register("update_task", "tasks.write", RiskWrite, LocationCloud, true, false)
	// HTTP is deliberately outbound-only. Branded provider callbacks are
	// private infrastructure and are never represented as workflow triggers.
	register("http_request", "http.write", RiskWrite, LocationCloud, false, true)
	register("delete_resource", "resources.delete", RiskDestructive, LocationEither, true, false)
	register("change_permissions", "permissions.write", RiskDestructive, LocationCloud, true, false)
	register("exact_tool", "tools.execute", RiskWrite, LocationEither, false, true)
	return registry
}

func coreNodeSchemas(kind string) (JSONSchema, JSONSchema) {
	object := JSONSchema{"type": "object"}
	switch kind {
	case "changed_files":
		return object, JSONSchema{"type": "object", "required": []string{"items", "claimed", "provenance"}, "properties": JSONSchema{
			"items": JSONSchema{"type": "array"}, "claimed": JSONSchema{"type": "integer"}, "provenance": JSONSchema{"type": "object"},
		}}
	case "read_content":
		contentRef := JSONSchema{"type": "object", "required": []string{"sourceKind", "providerId", "resourceId", "displayName", "permissionScope"}}
		return object, JSONSchema{"type": "object", "required": []string{"content", "sections", "citations", "truncated", "sourceChanged"}, "properties": JSONSchema{
			"content": contentRef, "sections": JSONSchema{"type": "array"}, "citations": JSONSchema{"type": "array"}, "nextCursor": JSONSchema{"type": "string"}, "truncated": JSONSchema{"type": "boolean"}, "sourceChanged": JSONSchema{"type": "boolean"},
		}}
	case "notify_private":
		return object, JSONSchema{"type": "object", "required": []string{"notified", "eventId"}, "properties": JSONSchema{"notified": JSONSchema{"type": "boolean"}, "eventId": JSONSchema{"type": "string"}}}
	case "memory_write":
		return object, JSONSchema{"type": "object", "required": []string{"written", "memoryEventId"}, "properties": JSONSchema{"written": JSONSchema{"type": "boolean"}, "memoryEventId": JSONSchema{"type": "integer"}}}
	case "post_reply":
		return object, JSONSchema{"type": "object", "required": []string{"posted", "messageId"}, "properties": JSONSchema{"posted": JSONSchema{"type": "boolean"}, "messageId": JSONSchema{"type": "string"}, "draft": JSONSchema{"type": "string"}}}
	case "task_query":
		return object, JSONSchema{"type": "object", "required": []string{"tasks"}, "properties": JSONSchema{"tasks": JSONSchema{"type": "array"}}}
	case "calendar_query":
		return object, JSONSchema{"type": "object", "required": []string{"events"}, "properties": JSONSchema{"events": JSONSchema{"type": "array"}}}
	case "create_task", "update_task":
		return object, JSONSchema{"type": "object", "required": []string{"task"}, "properties": JSONSchema{"task": JSONSchema{"type": "object"}}}
	default:
		return object, object
	}
}
