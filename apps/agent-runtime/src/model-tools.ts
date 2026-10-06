import { jsonSchema, tool, type ToolSet } from "ai";
import type { MCPRemoteTool } from "./types.js";

// Providers accept at most 128 tools per request and 64-character tool names.
const maxModelTools = 128;

/** The model sees Misty's tool names with dots as underscores. */
export function modelToolName(name: string): string {
  return name.replace(/[^A-Za-z0-9_-]/g, "_").slice(0, 64);
}

export interface ModelCatalog {
  /** Catalog tools the model sees, in catalog order. */
  tools: Array<MCPRemoteTool & { modelName: string }>;
  /** Tools past the provider limit. Misty's own tools come first in a catalog. */
  omitted: string[];
  mistyName(modelName: string): string;
  modelName(mistyName: string): string | undefined;
  readOnly(modelName: string): boolean;
}

/** Names every catalog tool once, avoiding the runtime's own tool names. */
export function modelCatalog(descriptors: MCPRemoteTool[], reserved: string[]): ModelCatalog {
  const taken = new Set(reserved);
  const visible = descriptors.slice(0, Math.max(0, maxModelTools - reserved.length));
  const tools = visible.map((descriptor) => {
    const base = modelToolName(descriptor.name);
    let candidate = base;
    for (let suffix = 2; taken.has(candidate); suffix++) candidate = `${base.slice(0, 60)}_${suffix}`;
    taken.add(candidate);
    return { ...descriptor, modelName: candidate };
  });
  const byModelName = new Map(tools.map((item) => [item.modelName, item]));
  const byMistyName = new Map(tools.map((item) => [item.name, item.modelName]));
  return {
    tools,
    omitted: descriptors.slice(visible.length).map((descriptor) => descriptor.name),
    mistyName: (name) => byModelName.get(name)?.name ?? name,
    modelName: (name) => byMistyName.get(name),
    readOnly: (name) => byModelName.get(name)?.readOnly === true,
  };
}

/** One AI SDK tool per catalog entry; every call runs through Misty's gateway. */
export function catalogTools(catalog: ModelCatalog, run: (callId: string, name: string, input: unknown) => Promise<unknown>): ToolSet {
  return Object.fromEntries(catalog.tools.map((descriptor) => [descriptor.modelName, tool({
    description: descriptor.description,
    inputSchema: jsonSchema(descriptor.inputSchema as Parameters<typeof jsonSchema>[0]),
    execute: (value, options) => run(options.toolCallId, descriptor.name, value),
    toModelOutput: ({ output }) => visualToolOutput(descriptor.name, output),
  })]));
}

type ContentPart = { type: "text"; text: string } | { type: "image-data"; data: string; mediaType: string };
export type ModelToolOutput = { type: "text"; value: string } | { type: "content"; value: ContentPart[] };

const imageDataURL = /^data:(image\/(?:png|jpeg|webp));base64,([A-Za-z0-9+/=]+)$/;

/** Screenshots return to the model as images rather than base64 text. */
export function visualToolOutput(name: string, output: unknown): ModelToolOutput {
  if ((name !== "browser.visual" && name !== "browser.workspace.visual" && name !== "browser.act") || !output || typeof output !== "object") {
    return { type: "text", value: JSON.stringify(output) ?? "null" };
  }
  const result = output as Record<string, unknown>;
  if (!Array.isArray(result.displayCaptures)) return imageOutput(result, JSON.stringify(output));
  const { displayCaptures, ...observed } = result;
  const content: ContentPart[] = [{ type: "text", text: JSON.stringify(observed) }];
  for (const capture of displayCaptures as Array<{ name: string; dataUrl: string; width: number; height: number }>) {
    const match = capture.dataUrl.match(imageDataURL);
    if (match?.[1] && match[2]) {
      content.push(
        { type: "text", text: `Fresh ${capture.name}: ${capture.width} x ${capture.height} screenshot pixels; top-left origin` },
        { type: "image-data", data: match[2], mediaType: match[1] },
      );
    }
  }
  return { type: "content", value: content };
}

/** Returns a result's `image` as an image part next to the rest of the result. */
export function imageOutput(output: Record<string, unknown>, fallback?: string): ModelToolOutput {
  const { image, ...observed } = output as { image?: { dataUrl?: string }; [key: string]: unknown };
  const match = image?.dataUrl?.match(imageDataURL);
  if (match?.[1] && match[2]) {
    return { type: "content", value: [{ type: "text", text: JSON.stringify(observed) }, { type: "image-data", data: match[2], mediaType: match[1] }] };
  }
  return { type: "text", value: fallback ?? JSON.stringify(observed) };
}
