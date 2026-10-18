// web.fetch for agent runs with no device online (see README.md). POST /fetch
// with {url, question} and the shared secret; answers {url, status, title, text,
// truncated} or {code} with the same failure codes the desktop uses.

interface Env {
  MISTY_WEB_FETCH_SECRET: string;
}

const maxBytes = 2 * 1024 * 1024;
const maxRedirects = 5;
const textLimit = 15_000;

const failure = (code: string, status = 422) => Response.json({ code }, { status });

function sameSecret(given: string, expected: string): boolean {
  if (expected.length < 32 || given.length !== expected.length) return false;
  let diff = 0;
  for (let index = 0; index < given.length; index++) diff |= given.charCodeAt(index) ^ expected.charCodeAt(index);
  return diff === 0;
}

/** Workers can't resolve names, and their egress reaches only the public internet; literal private hosts are still refused. */
function publicAddress(raw: string): URL | null {
  let url: URL;
  try {
    url = new URL(raw);
  } catch {
    return null;
  }
  if (!["http:", "https:"].includes(url.protocol) || url.username || url.password) return null;
  const host = url.hostname.toLowerCase().replace(/^\[|\]$/g, "");
  if (host === "localhost" || host.endsWith(".localhost") || host.endsWith(".local") || host.endsWith(".internal")) return null;
  const v4 = host.match(/^(\d+)\.(\d+)\.(\d+)\.(\d+)$/);
  if (v4) {
    const [a, b] = [Number(v4[1]), Number(v4[2])];
    if (a === 10 || a === 127 || a === 0 || a >= 224 || (a === 169 && b === 254) || (a === 172 && b >= 16 && b < 32) || (a === 192 && b === 168) || (a === 100 && b >= 64 && b < 128)) return null;
  }
  if (host.includes(":") && (host === "::1" || host === "::" || /^f[cd]/.test(host) || /^fe[89ab]/.test(host) || host.startsWith("::ffff:"))) return null;
  return url;
}

const readableType = (type: string) =>
  type === "" || ["text/html", "application/xhtml+xml", "text/plain", "text/markdown", "application/json", "application/xml", "text/xml"].some((kind) => type.startsWith(kind));

async function body(response: Response): Promise<{ text: string; truncated: boolean }> {
  const reader = response.body?.getReader();
  if (!reader) return { text: "", truncated: false };
  const chunks: Uint8Array[] = [];
  let size = 0;
  for (;;) {
    const { done, value } = await reader.read();
    if (done) break;
    if (size + value.length >= maxBytes) {
      chunks.push(value.slice(0, maxBytes - size));
      await reader.cancel();
      return { text: new TextDecoder().decode(concat(chunks)), truncated: true };
    }
    chunks.push(value);
    size += value.length;
  }
  return { text: new TextDecoder().decode(concat(chunks)), truncated: false };
}

function concat(chunks: Uint8Array[]): Uint8Array {
  const out = new Uint8Array(chunks.reduce((sum, chunk) => sum + chunk.length, 0));
  let offset = 0;
  for (const chunk of chunks) {
    out.set(chunk, offset);
    offset += chunk.length;
  }
  return out;
}

const skipped = "script,style,noscript,template,svg,canvas,iframe,nav,header,footer,aside,select";
const blockTags = new Set([
  "address", "article", "blockquote", "br", "button", "caption", "dd", "details", "div", "dl", "dt", "fieldset", "figcaption",
  "figure", "form", "h1", "h2", "h3", "h4", "h5", "h6", "hr", "label", "legend", "li", "main", "ol", "p", "pre", "section",
  "summary", "table", "tbody", "thead", "tr", "ul",
]);
const voidTags = new Set(["br", "hr", "img", "input", "meta", "link", "source", "wbr"]);

/**
 * Every visible text as one plain line per block (headings and list items keep their Markdown
 * marks, table rows join their cells with "|"), through the streaming HTML rewriter. Prices
 * that sit in plain divs and spans, as on most pricing pages, are kept; text marked
 * aria-hidden is a drawn copy of text a screen reader gets elsewhere, so it is skipped.
 */
async function readableLines(html: string): Promise<{ title: string; lines: string[] }> {
  const lines: string[] = [];
  let title = "";
  let skipDepth = 0;
  let buffer = "";
  let prefix = "";
  const flush = () => {
    const text = buffer.replace(/\s+/g, " ").replace(/(\s*\|)+\s*$/, "").trim();
    buffer = "";
    if (!text || /^[\s|\-–—•·]*$/.test(text)) {
      prefix = "";
      return;
    }
    if (lines[lines.length - 1] !== prefix + text) lines.push(prefix + text);
    prefix = "";
  };
  const rewriter = new HTMLRewriter()
    // The document's own title only: inline SVGs carry <title> elements too.
    .on("head > title", { text: (chunk) => void (title += chunk.text) })
    .on("*", {
      element: (element) => {
        const tag = element.tagName.toLowerCase();
        const hidden = element.hasAttribute("hidden") || element.getAttribute("aria-hidden") === "true";
        const skip = hidden || skipped.split(",").includes(tag);
        if (voidTags.has(tag)) {
          if (!skip && blockTags.has(tag)) flush();
          return;
        }
        const block = blockTags.has(tag);
        if (!skip && block) {
          flush();
          if (/^h[1-6]$/.test(tag)) prefix = `${"#".repeat(Number(tag[1]))} `;
          else if (tag === "li") prefix = "- ";
        }
        try {
          element.onEndTag(() => {
            if (skip) skipDepth--;
            else if (tag === "td" || tag === "th") buffer += " | ";
            if (!skip && block) flush();
          });
          if (skip) skipDepth++;
        } catch {
          // An element with no end tag (self-closed or left open) has nothing to skip or close.
        }
      },
      text: (chunk) => {
        if (!skipDepth) buffer += chunk.text;
      },
    });
  await rewriter.transform(new Response(html)).text();
  flush();
  return { title: title.replace(/\s+/g, " ").trim(), lines };
}

/** Pricing pages draw one billing period until a toggle is switched, and a fetch can't switch it. */
function billingNote(text: string): string | undefined {
  if (!/[$€£]\s?\d/.test(text) || !/\b(annual(ly)?|yearly|per year|billed monthly|month-to-month|monthly)\b/i.test(text)) return undefined;
  return "Prices here show the billing period the page opens on (often yearly) and the page's monthly/yearly toggle cannot be switched by a fetch. State which period each price is for. To read the other period, open the page in the browser and switch the toggle.";
}

const words = (text: string) => new Set(text.toLowerCase().match(/[\p{L}\p{N}]{3,}/gu) ?? []);

/** The lines that best match the question, with their neighbours, in page order. */
function relevant(all: string[], question: string): string[] {
  const wanted = words(question);
  const scored = all.map((line, index) => {
    let score = 0;
    for (const word of words(line)) if (wanted.has(word)) score++;
    return { index, score: line.startsWith("#") ? score + 0.5 : score };
  });
  const keep = new Set<number>();
  let size = 0;
  for (const { index, score } of [...scored].sort((a, b) => b.score - a.score || a.index - b.index)) {
    if (score <= 0 || size >= textLimit) break;
    for (const neighbour of [index - 1, index, index + 1]) {
      const line = all[neighbour];
      if (line === undefined || keep.has(neighbour)) continue;
      keep.add(neighbour);
      size += line.length + 1;
    }
  }
  return [...keep].sort((a, b) => a - b).map((index) => all[index]!);
}

async function fetchPage(address: string, question: string): Promise<Response> {
  let url = publicAddress(address);
  if (!url) return failure("invalid_url");
  for (let hop = 0; hop <= maxRedirects; hop++) {
    let response: Response;
    try {
      response = await fetch(url, {
        redirect: "manual",
        signal: AbortSignal.timeout(20_000),
        headers: { "User-Agent": "Mozilla/5.0 (compatible; MistyFetch/1)", Accept: "text/html,application/xhtml+xml,text/plain,application/json;q=0.9,*/*;q=0.5" },
      });
    } catch (error) {
      return failure(String(error).includes("Timeout") ? "fetch_timeout" : "fetch_failed", 502);
    }
    if (response.status >= 300 && response.status < 400) {
      const next = response.headers.get("Location");
      url = next ? publicAddress(new URL(next, url).toString()) : null;
      if (!url) return failure("private_address");
      continue;
    }
    const type = (response.headers.get("Content-Type") ?? "").toLowerCase();
    if (!readableType(type)) return failure("unsupported_content");
    const page = await body(response);
    let title = "";
    let text = page.text.trim();
    let truncated = page.truncated;
    if (/html|xml/.test(type) || type === "") {
      const read = await readableLines(page.text);
      title = read.title;
      const full = read.lines.join("\n");
      if (full.length > textLimit) {
        truncated = true;
        const picked = question.trim() ? relevant(read.lines, question) : [];
        text = (picked.length ? picked.join("\n") : full).slice(0, textLimit);
      } else text = full;
    } else if (text.length > textLimit) {
      text = text.slice(0, textLimit);
      truncated = true;
    }
    return Response.json({ url: url.toString(), status: response.status, title, text, truncated, note: billingNote(text) });
  }
  return failure("too_many_redirects");
}

export default {
  async fetch(request: Request, env: Env): Promise<Response> {
    const path = new URL(request.url).pathname;
    if (request.method !== "POST" || path !== "/fetch") return new Response("Not found", { status: 404 });
    if (!sameSecret((request.headers.get("Authorization") ?? "").replace(/^Bearer /, ""), env.MISTY_WEB_FETCH_SECRET ?? "")) return new Response("Unauthorized", { status: 401 });
    let input: { url?: unknown; question?: unknown };
    try {
      input = await request.json();
    } catch {
      return failure("invalid_url", 400);
    }
    return fetchPage(String(input.url ?? ""), typeof input.question === "string" ? input.question : "");
  },
} satisfies ExportedHandler<Env>;
