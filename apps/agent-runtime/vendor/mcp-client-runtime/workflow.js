// Workflow orchestration never executes MCP transport code. Step compilation
// resolves the `node` condition to the real SDK; this condition prevents the
// deterministic workflow bundle from pulling Node/OAuth dependencies into its
// isolate merely because it imports a step module.
export class Client {
  constructor() {
    throw new Error("MCP Client is only available inside a workflow step.");
  }
}

export class StreamableHTTPClientTransport {
  constructor() {
    throw new Error("MCP transport is only available inside a workflow step.");
  }
}
