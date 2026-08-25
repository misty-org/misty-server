export function resolveMCPEndpoint(
  controlPlaneURL: string,
  mcpPath: string,
): URL {
  if (!mcpPath.startsWith("/")) {
    throw new Error("Misty returned an invalid MCP path");
  }
  const base = new URL(controlPlaneURL);
  base.pathname = `${base.pathname.replace(/\/$/, "")}/`;
  return new URL(mcpPath.replace(/^\/+/, ""), base);
}
