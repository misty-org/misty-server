// Counts the request asks for, checked against the answer without a model call
// (docs/plans/agent-harness-v3.md, change 4). A run reported "completed" with 8 of the 10
// pediatricians asked for, and the model review let it stand (Odysseys, October 20).
//
// For each "at least N things" (or "N things", "around N things") in the request, the answer's
// section whose heading names those things must list at least N items: list lines or table rows.
// A request count the answer has no section for is left to the model review.

const words: Record<string, number> = { two: 2, three: 3, four: 4, five: 5, six: 6, seven: 7, eight: 8, nine: 9, ten: 10, twelve: 12, fifteen: 15, twenty: 20 };
const asked = /\b(?:at least|minimum of|around|about|exactly|find|list|compare|open)?\s*(\d{1,2}|two|three|four|five|six|seven|eight|nine|ten|twelve|fifteen|twenty)\s+(?:(?:different|distinct|separate|more|real|major|top|best|relevant|qualifying|[a-z]+-[a-z]+)\s+)?([a-z][a-z-]{3,})/gi;
/** Words that follow a number without naming things to list. */
const notThings = /^(minutes?|hours?|days?|weeks?|months?|years?|times?|steps?|percent|dollars?|stars?|miles?|tabs?|pages?|sections?|sentences?|words?|points?|options?)$/i;

export interface CountRequirement {
  count: number;
  thing: string;
}

const stem = (word: string) => word.toLowerCase().replace(/(ies|es|s)$/, "").slice(0, 6);

/** The counts a request asks for, at most one per thing, the largest. */
export function requestedCounts(request: string): CountRequirement[] {
  const found = new Map<string, CountRequirement>();
  for (const match of request.matchAll(asked)) {
    const count = Number(match[1]) || words[match[1]!.toLowerCase()] || 0;
    const thing = match[2] && !notThings.test(match[2]) ? match[2] : "";
    if (count < 3 || count > 50 || !thing) continue;
    const key = stem(thing);
    if ((found.get(key)?.count ?? 0) < count) found.set(key, { count, thing });
  }
  return [...found.values()];
}

interface Section {
  heading: string;
  items: number;
}

/** The answer's sections: a heading (markdown or a bold line) and the list items under it. */
function sections(answer: string): Section[] {
  const out: Section[] = [];
  let current: Section | undefined;
  for (const line of answer.split(/\r?\n/)) {
    const heading = /^\s*(#{1,6}\s+(.+)|\*\*([^*]{3,120})\*\*:?\s*$)/.exec(line);
    if (heading) {
      current = { heading: (heading[2] ?? heading[3] ?? "").toLowerCase(), items: 0 };
      out.push(current);
      continue;
    }
    if (!current) continue;
    const listItem = /^\s*(?:[-*+]|\d+[.)])\s+\S/.test(line);
    const tableRow = /^\s*\|.*\|\s*$/.test(line) && !/^\s*\|[\s:|-]+\|\s*$/.test(line);
    if (listItem || tableRow) current.items++;
  }
  return out;
}

/** Requested counts the answer's matching section falls short of. */
export function countShortfalls(request: string, answer: string): Array<CountRequirement & { listed: number }> {
  const parts = sections(answer);
  const short: Array<CountRequirement & { listed: number }> = [];
  for (const requirement of requestedCounts(request)) {
    const key = stem(requirement.thing);
    const matching = parts.filter((section) => section.heading.split(/[^a-z]+/).some((word) => word.length > 3 && stem(word) === key));
    if (!matching.length) continue;
    // A table's header row counts as a row, so one short is let through.
    const listed = Math.max(...matching.map((section) => section.items));
    if (listed > 0 && listed + 1 < requirement.count) short.push({ ...requirement, listed });
  }
  return short;
}

/** The note for a finish short of a requested count, or undefined. */
export function countRefusal(request: string, answer: string): string | undefined {
  const short = countShortfalls(request, answer);
  if (!short.length) return undefined;
  const lines = short.map((item) => `the request asks for ${item.count} ${item.thing} and your answer lists ${item.listed}`);
  return `Counts: ${lines.join("; ")}. Find and add the rest from pages you open, or say in remaining exactly how many you could confirm and why the others are missing.`;
}
