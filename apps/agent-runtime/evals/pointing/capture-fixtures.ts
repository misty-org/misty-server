// Makes pointing fixtures from the screen as it is now (macOS only).
//
//   node evals/pointing/capture-fixtures.ts --app Xcode --delay 5 --limit 25
//   (add --image screen.png to use a screenshot of the main display made elsewhere)
//
// Bring the app to the front during the delay. The main display is captured
// at the size the companion sends, and the app's named controls become
// questions whose answer box is the control's own Accessibility frame. The
// terminal running this needs Screen Recording and Accessibility access.
// Fixtures show your screen; they stay out of git (see .gitignore).
import { execFileSync } from "node:child_process";
import { mkdirSync, rmSync, writeFileSync } from "node:fs";
import { tmpdir } from "node:os";
import { join, resolve } from "node:path";

interface Control {
  role: string;
  name: string;
  x: number;
  y: number;
  width: number;
  height: number;
}

function option(name: string, fallback: string) {
  const index = process.argv.indexOf(`--${name}`);
  return index >= 0 && process.argv[index + 1] ? process.argv[index + 1]! : fallback;
}

/** Same bounds as the desktop's capture (MistyCompanionCapture.m). */
export function captureSize(width: number, height: number) {
  const scale = Math.min(1, 768 / Math.min(width, height), 1568 / Math.max(width, height));
  return {
    width: Math.max(1, Math.floor(width * scale)),
    height: Math.max(1, Math.floor(height * scale)),
  };
}

/** A control's frame in the captured image's pixels, kept inside the image. */
export function targetBox(
  control: Control,
  scale: number,
  size: { width: number; height: number },
) {
  const x = Math.min(size.width - 1, Math.max(0, Math.round(control.x * scale)));
  const y = Math.min(size.height - 1, Math.max(0, Math.round(control.y * scale)));
  return {
    x,
    y,
    width: Math.max(1, Math.min(size.width - x, Math.round(control.width * scale))),
    height: Math.max(1, Math.min(size.height - y, Math.round(control.height * scale))),
  };
}

const QUESTIONS: Record<string, (name: string) => string> = {
  AXMenuBarItem: (name) => `where is the ${name} menu?`,
  AXButton: (name) => `where do I click ${name}?`,
  AXCheckBox: (name) => `where is the ${name} checkbox?`,
  AXRadioButton: (name) => `where is the ${name} option?`,
  AXPopUpButton: (name) => `where do I choose ${name}?`,
  AXMenuButton: (name) => `where is the ${name} menu button?`,
  AXTextField: (name) => `where do I type the ${name}?`,
  AXSearchField: () => `where do I search?`,
  AXLink: (name) => `where is the ${name} link?`,
  AXTab: (name) => `where is the ${name} tab?`,
};

// Runs in osascript's JavaScript; reads the frontmost process's menu bar and
// its first window's controls through System Events.
const listControls = `
ObjC.import("AppKit");
const frame = $.NSScreen.mainScreen.frame;
const process = Application("System Events").processes.whose({ frontmost: true })[0];
const out = [];
const add = (element) => {
  try {
    const role = element.role();
    const name = element.name() || element.description() || "";
    const [x, y] = element.position();
    const [width, height] = element.size();
    if (name && width > 0 && height > 0) out.push({ role, name, x, y, width, height });
  } catch (error) {}
};
try { process.menuBars[0].menuBarItems().forEach(add); } catch (error) {}
try { process.windows[0].entireContents().slice(0, 1500).forEach(add); } catch (error) {}
JSON.stringify({ app: process.name(), width: frame.size.width, height: frame.size.height, controls: out });
`;

function main() {
  if (process.platform !== "darwin") throw new Error("Fixture capture needs macOS.");
  const out = resolve(option("out", join(import.meta.dirname, "fixtures")));
  const limit = Number(option("limit", "25"));
  const delay = Number(option("delay", "5"));
  const prefix = option("prefix", `capture-${Date.now()}`);
  console.log(`Bring the app to the front; capturing in ${delay}s…`);
  execFileSync("sleep", [String(delay)]);
  const listed = JSON.parse(
    execFileSync("osascript", ["-l", "JavaScript", "-e", listControls], { encoding: "utf8" }),
  ) as { app: string; width: number; height: number; controls: Control[] };
  const wanted = option("app", "");
  if (wanted && listed.app !== wanted)
    throw new Error(`The frontmost app is ${listed.app}, not ${wanted}.`);
  const size = captureSize(listed.width, listed.height);
  const scale = size.width / listed.width;
  mkdirSync(out, { recursive: true });
  // --image takes a screenshot of the main display made another way, for a
  // terminal without Screen Recording access.
  const given = option("image", "");
  const raw = given ? resolve(given) : join(tmpdir(), `${prefix}.png`);
  const image = `${prefix}.jpg`;
  if (!given) execFileSync("screencapture", ["-x", "-m", "-t", "png", raw]);
  execFileSync(
    "sips",
    [
      "-s",
      "format",
      "jpeg",
      "-z",
      String(size.height),
      String(size.width),
      raw,
      "--out",
      join(out, image),
    ],
    {
      stdio: "ignore",
    },
  );
  if (!given) rmSync(raw, { force: true });
  const seen = new Set<string>();
  const usable = listed.controls.filter((control) => {
    const ask = QUESTIONS[control.role];
    const key = `${control.role}:${control.name}`;
    const inside =
      control.x >= 0 &&
      control.y >= 0 &&
      control.x + control.width <= listed.width &&
      control.y + control.height <= listed.height;
    const sized =
      control.width * scale >= 6 &&
      control.height * scale >= 6 &&
      control.width <= 480 &&
      control.height <= 240;
    if (!ask || !inside || !sized || seen.has(key)) return false;
    seen.add(key);
    return true;
  });
  const step = Math.max(1, usable.length / limit);
  const chosen = Array.from(
    { length: Math.min(limit, usable.length) },
    (_, index) => usable[Math.floor(index * step)]!,
  );
  chosen.forEach((control, index) => {
    const fixture = {
      id: `${prefix}-${index + 1}`,
      image,
      mimeType: "image/jpeg",
      width: size.width,
      height: size.height,
      question: QUESTIONS[control.role]!(control.name),
      target: targetBox(control, scale, size),
      source: { app: listed.app, role: control.role, name: control.name },
    };
    writeFileSync(join(out, `${fixture.id}.json`), JSON.stringify(fixture, null, 2));
  });
  console.log(`Wrote ${chosen.length} fixtures from ${listed.app} to ${out}.`);
}

if (import.meta.url === `file://${process.argv[1]}`) main();
