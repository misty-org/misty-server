import * as Y from "yjs";

import { isRecord } from "./control-protocol";

const ELEMENT_TYPES = new Set([
  "rectangle", "diamond", "ellipse", "text", "line", "arrow", "freedraw",
  "image", "frame", "magicframe", "iframe", "embeddable",
]);
const MAX_ELEMENTS_PER_APPLY = 500;
const MAX_COORDINATE = 1_000_000;

export type DrawingSceneMutation = {
  elements: Record<string, unknown>[];
  deleted: number;
  changed: number;
  scene: Record<string, unknown>;
};

function finiteNumber(value: unknown, fallback: number, minimum = -MAX_COORDINATE, maximum = MAX_COORDINATE): number {
  return typeof value === "number" && Number.isFinite(value)
    ? Math.max(minimum, Math.min(maximum, value))
    : fallback;
}

function positiveInteger(value: unknown, fallback: number): number {
  return Number.isInteger(value) && Number(value) > 0
    ? Math.min(Number(value), 2_147_483_646)
    : fallback;
}

function randomInteger(): number {
  const bytes = crypto.getRandomValues(new Uint32Array(1));
  return 1 + (bytes[0]! % 2_147_483_645);
}

function points(value: unknown, fallback: number[][]): number[][] {
  if (!Array.isArray(value) || value.length < 2 || value.length > 10_000) return fallback;
  const normalized: number[][] = [];
  for (const point of value) {
    if (!Array.isArray(point) || point.length !== 2) return fallback;
    normalized.push([
      finiteNumber(point[0], 0),
      finiteNumber(point[1], 0),
    ]);
  }
  return normalized;
}

function normalizeElement(
  input: Record<string, unknown>,
  current: Record<string, unknown> | undefined,
  now: number,
): Record<string, unknown> {
  const merged = { ...(current ?? {}), ...input };
  const id = typeof merged.id === "string" ? merged.id.trim() : "";
  const type = typeof merged.type === "string" ? merged.type : "";
  if (!id || id.length > 128 || !/^[A-Za-z0-9_-]+$/u.test(id) || !ELEMENT_TYPES.has(type)) {
    throw new Error("invalid_drawing_element");
  }
  let fallbackWidth = 100;
  let fallbackHeight = 100;
  let normalizedPoints: number[][] | null = null;
  if (type === "text") {
    const text = typeof merged.text === "string" ? merged.text : "";
    const fontSize = finiteNumber(merged.fontSize, 20, 1, 512);
    const lines = text.split("\n");
    fallbackWidth = Math.max(10, Math.min(MAX_COORDINATE, Math.max(...lines.map((line) => line.length), 1) * fontSize * 0.62));
    fallbackHeight = Math.max(fontSize, lines.length * fontSize * 1.25);
  } else if (type === "line" || type === "arrow" || type === "freedraw") {
    normalizedPoints = points(merged.points, [[0, 0], [100, 0]]);
    const xs = normalizedPoints.map((point) => point[0]!);
    const ys = normalizedPoints.map((point) => point[1]!);
    fallbackWidth = Math.max(...xs) - Math.min(...xs);
    fallbackHeight = Math.max(...ys) - Math.min(...ys);
  }
  const width = finiteNumber(merged.width, fallbackWidth, 0, MAX_COORDINATE);
  const height = finiteNumber(merged.height, fallbackHeight, 0, MAX_COORDINATE);
  const element: Record<string, unknown> = {
    ...merged,
    id,
    type,
    x: finiteNumber(merged.x, 0),
    y: finiteNumber(merged.y, 0),
    strokeColor: typeof merged.strokeColor === "string" ? merged.strokeColor : "#1e1e1e",
    backgroundColor: typeof merged.backgroundColor === "string" ? merged.backgroundColor : "transparent",
    fillStyle: ["hachure", "cross-hatch", "solid", "zigzag"].includes(String(merged.fillStyle)) ? merged.fillStyle : "solid",
    strokeWidth: finiteNumber(merged.strokeWidth, 2, 0, 20),
    strokeStyle: ["solid", "dashed", "dotted"].includes(String(merged.strokeStyle)) ? merged.strokeStyle : "solid",
    roundness: merged.roundness === null || isRecord(merged.roundness) ? merged.roundness : null,
    roughness: finiteNumber(merged.roughness, 1, 0, 3),
    opacity: finiteNumber(merged.opacity, 100, 0, 100),
    width,
    height,
    angle: finiteNumber(merged.angle, 0, -Math.PI * 2, Math.PI * 2),
    seed: positiveInteger(merged.seed, randomInteger()),
    version: positiveInteger(current?.version, 0) + 1,
    versionNonce: randomInteger(),
    index: typeof merged.index === "string" ? merged.index : null,
    isDeleted: merged.isDeleted === true,
    groupIds: Array.isArray(merged.groupIds) ? merged.groupIds.filter((value): value is string => typeof value === "string").slice(0, 100) : [],
    frameId: typeof merged.frameId === "string" ? merged.frameId : null,
    boundElements: Array.isArray(merged.boundElements) ? merged.boundElements.slice(0, 100) : null,
    updated: now,
    link: typeof merged.link === "string" ? merged.link.slice(0, 4096) : null,
    locked: merged.locked === true,
  };

  if (type === "text") {
    const text = typeof merged.text === "string" ? merged.text.slice(0, 100_000) : "";
    const fontSize = finiteNumber(merged.fontSize, 20, 1, 512);
    element.text = text;
    element.originalText = typeof merged.originalText === "string" ? merged.originalText.slice(0, 100_000) : text;
    element.fontSize = fontSize;
    element.fontFamily = positiveInteger(merged.fontFamily, 5);
    element.textAlign = ["left", "center", "right"].includes(String(merged.textAlign)) ? merged.textAlign : "left";
    element.verticalAlign = ["top", "middle", "bottom"].includes(String(merged.verticalAlign)) ? merged.verticalAlign : "top";
    element.containerId = typeof merged.containerId === "string" ? merged.containerId : null;
    element.autoResize = merged.autoResize !== false;
    element.lineHeight = finiteNumber(merged.lineHeight, 1.25, 0.5, 4);
  } else if (type === "line" || type === "arrow") {
    element.points = normalizedPoints ?? [[0, 0], [width || 100, height]];
    element.lastCommittedPoint = null;
    element.startBinding = isRecord(merged.startBinding) ? merged.startBinding : null;
    element.endBinding = isRecord(merged.endBinding) ? merged.endBinding : null;
    element.startArrowhead = typeof merged.startArrowhead === "string" ? merged.startArrowhead : null;
    element.endArrowhead = typeof merged.endArrowhead === "string" ? merged.endArrowhead : type === "arrow" ? "arrow" : null;
    if (type === "arrow") element.elbowed = merged.elbowed === true;
  } else if (type === "freedraw") {
    normalizedPoints ??= [[0, 0], [width || 1, height || 1]];
    element.points = normalizedPoints;
    element.pressures = Array.isArray(merged.pressures)
      ? merged.pressures.slice(0, normalizedPoints.length).map((value) => finiteNumber(value, 0.5, 0, 1))
      : [];
    element.simulatePressure = merged.simulatePressure !== false;
    element.lastCommittedPoint = null;
  } else if (type === "image") {
    element.fileId = typeof merged.fileId === "string" ? merged.fileId : null;
    element.status = ["pending", "saved", "error"].includes(String(merged.status)) ? merged.status : element.fileId ? "saved" : "pending";
    element.scale = Array.isArray(merged.scale) && merged.scale.length === 2 ? merged.scale : [1, 1];
    element.crop = isRecord(merged.crop) ? merged.crop : null;
  } else if (type === "frame" || type === "magicframe") {
    element.name = typeof merged.name === "string" ? merged.name.slice(0, 500) : null;
  }
  return element;
}

function sortedElements(doc: Y.Doc, includeDeleted = true): Record<string, unknown>[] {
  return Array.from(doc.getMap<Record<string, unknown>>("drawing:elements").values())
    .filter((element) => includeDeleted || element.isDeleted !== true)
    .sort((left, right) => String(left.index ?? "").localeCompare(String(right.index ?? "")) || String(left.id).localeCompare(String(right.id)));
}

export function drawingSceneState(doc: Y.Doc, includeDeleted = false): Record<string, unknown> {
  return {
    elements: sortedElements(doc, includeDeleted),
    scene: Object.fromEntries(doc.getMap<unknown>("drawing:scene")),
    files: Object.fromEntries(doc.getMap<unknown>("drawing:files")),
  };
}

export function buildDrawingSceneMutation(
  doc: Y.Doc,
  payload: Record<string, unknown>,
  now = Date.now(),
): DrawingSceneMutation {
  const rawElements = payload.elements ?? [];
  const rawDeletes = payload.delete_element_ids ?? [];
  if (!Array.isArray(rawElements) || rawElements.length > MAX_ELEMENTS_PER_APPLY || !Array.isArray(rawDeletes) || rawDeletes.length > MAX_ELEMENTS_PER_APPLY) {
    throw new Error("invalid_drawing_scene");
  }
  const current = doc.getMap<Record<string, unknown>>("drawing:elements");
  const next = new Map<string, Record<string, unknown>>();
  for (const [id, element] of current) next.set(id, element);
  let changed = 0;
  for (const raw of rawElements) {
    if (!isRecord(raw)) throw new Error("invalid_drawing_element");
    const id = typeof raw.id === "string" ? raw.id.trim() : "";
    const normalized = normalizeElement(raw, next.get(id), now);
    next.set(id, normalized);
    changed += 1;
  }
  const deleteIDs = new Set(rawDeletes.filter((value): value is string => typeof value === "string"));
  if (payload.mode === "replace") {
    const retained = new Set(rawElements.map((value) => isRecord(value) && typeof value.id === "string" ? value.id.trim() : ""));
    for (const [id, element] of next) if (!retained.has(id) && element.isDeleted !== true) deleteIDs.add(id);
  } else if (payload.mode !== undefined && payload.mode !== "merge") {
    throw new Error("invalid_drawing_scene");
  }
  let deleted = 0;
  for (const id of deleteIDs) {
    const element = next.get(id);
    if (!element || element.isDeleted === true) continue;
    next.set(id, normalizeElement({ id, isDeleted: true }, element, now));
    deleted += 1;
  }
  const scene: Record<string, unknown> = {};
  if (payload.scene !== undefined) {
    if (!isRecord(payload.scene)) throw new Error("invalid_drawing_scene");
    if (typeof payload.scene.viewBackgroundColor === "string" && payload.scene.viewBackgroundColor.length <= 100) {
      scene.viewBackgroundColor = payload.scene.viewBackgroundColor;
    }
  }
  return { elements: Array.from(next.values()), changed, deleted, scene };
}

export function applyDrawingSceneMutation(doc: Y.Doc, mutation: DrawingSceneMutation): void {
  doc.transact(() => {
    const elements = doc.getMap<Record<string, unknown>>("drawing:elements");
    for (const element of mutation.elements) elements.set(String(element.id), element);
    const scene = doc.getMap<unknown>("drawing:scene");
    for (const [key, value] of Object.entries(mutation.scene)) scene.set(key, value);
  }, "misty:drawing-control");
}

export async function drawingSceneHash(doc: Y.Doc): Promise<string> {
  const payload = new TextEncoder().encode(JSON.stringify(drawingSceneState(doc, true)));
  const digest = new Uint8Array(await crypto.subtle.digest("SHA-256", payload));
  return Array.from(digest, (byte) => byte.toString(16).padStart(2, "0")).join("");
}
