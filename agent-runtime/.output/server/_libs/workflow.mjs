import { createRequire as __wkfCreateRequire } from "node:module";
if (typeof globalThis.require === "undefined") globalThis.require = __wkfCreateRequire(import.meta.url);
import "./@workflow/core+[...].mjs";
//#region ../node_modules/workflow/dist/stdlib.js
/**
* This is the "standard library" of steps that we make available to all workflow users.
* The can be imported like so: `import { fetch } from 'workflow'`. and used in workflow.
* The need to be exported directly in this package and cannot live in `core` to prevent
* circular dependencies post-compilation.
*/ /**
* A hoisted `fetch()` function that is executed as a "step" function,
* for use within workflow functions.
*
* @see https://developer.mozilla.org/en-US/docs/Web/API/Fetch_API
*/ async function fetch(...args) {
	return globalThis.fetch(...args);
}
fetch.stepId = "step//workflow@4.8.3//fetch";
//#endregion
export {};
