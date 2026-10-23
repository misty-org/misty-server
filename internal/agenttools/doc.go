// Package agenttools defines Misty's authoritative Agent Toolbox contract.
//
// A Registry owns stable tool metadata, aliases, and the one handler bound for
// each action. Tools run without a per-action approval: agents share their
// owner's access. Resolve builds a context-specific model
// manifest, while Execute repeats context and authorization checks immediately
// before calling the handler. Product surfaces may bind different domain
// adapters and request different subsets, while sharing authoritative action
// descriptors from the product catalog.
//
// Domain handlers remain responsible for validating domain invariants and
// recording durable audit events. The Toolbox policies narrow access; they
// never replace authorization in the underlying domain service.
package agenttools
