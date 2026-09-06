// Registration inventory only: no HTTP requests, service calls, sockets or database access.
// Run after npm run build; a declared route is not evidence of complete integration.
import { readFile, writeFile } from "node:fs/promises";
import { fileURLToPath } from "node:url";
import { join } from "node:path";
import ts from "typescript";
import { pino } from "pino";
import { createApi } from "../../dist/apps/api/src/app.js";
import { createOfficialCatalog } from "../../dist/apps/api/src/modules/official-apps/catalog.js";
import { plannerRpcMethods } from "../../dist/apps/api/src/modules/planner/routes.js";
import { journalRpcMethods } from "../../dist/apps/api/src/modules/journal/routes.js";
import { connectionRpcMethods } from "../../dist/apps/api/src/modules/connections/routes.js";
import { mailRpcMethods } from "../../dist/apps/api/src/modules/mail/routes.js";
import { mistyServerMethods } from "@misty/contracts";

const root = fileURLToPath(new URL("../../", import.meta.url));
const parse = async path => ts.createSourceFile(path, await readFile(join(root, path), "utf8"), ts.ScriptTarget.Latest, true);
const composition = await parse("apps/api/src/main.ts"), apiSource = await parse("apps/api/src/app.ts");
const names = new Set(), mainNames = new Set();
for (const statement of apiSource.statements) if (ts.isInterfaceDeclaration(statement) && statement.name.text === "ApiDependencies") {
  for (const member of statement.members) if (member.questionToken && member.name) names.add(member.name.getText(apiSource));
}
function properties(expression) {
  if (!expression) return;
  if (ts.isParenthesizedExpression(expression)) return properties(expression.expression);
  if (ts.isConditionalExpression(expression)) { properties(expression.whenTrue); properties(expression.whenFalse); return; }
  if (!ts.isObjectLiteralExpression(expression)) throw new Error("Review changed createApi composition before inventorying it");
  for (const item of expression.properties) {
    if (ts.isSpreadAssignment(item)) properties(item.expression);
    else if (item.name) mainNames.add(item.name.getText(composition));
  }
}
function visit(node) {
  if (ts.isCallExpression(node) && node.expression.getText(composition) === "createApi") properties(node.arguments[0]);
  ts.forEachChild(node, visit);
}
visit(composition);
if (!mainNames.has("auth")) throw new Error("Cannot identify API composition");
const unavailable = new Proxy(() => { throw new Error("Inventory attempted to call a service"); }, {
  get: (_target, key) => key === Symbol.iterator ? function* () {} : unavailable,
});
function registrations(enabled, deployment) {
  const dependencies = Object.fromEntries([...enabled].filter(name => names.has(name)).map(name => [name, unavailable]));
  if (enabled.has("auth")) dependencies.auth = { service: unavailable, boundary: { middleware: async (_c, next) => next() }, deployment,
    ...(deployment === "hosted" ? { recovery: unavailable, handoff: unavailable } : {}) };
  if (enabled.has("onboarding")) dependencies.onboarding = { auth: unavailable, repository: unavailable, catalog: createOfficialCatalog() };
  if (enabled.has("runtimeCallbacks")) dependencies.runtimeCallbacks = { secrets: [], repository: unavailable };
  return [...new Set(createApi({ ...dependencies, logger: pino({ level: "silent" }), checkDatabase: async () => {}, isDraining: () => false, migrationComplete: false })
    .routes.filter(route => route.method !== "ALL").map(route => `${route.method} ${route.path}`))].sort();
}
const canonical = route => route.replace(/:[A-Za-z_][A-Za-z0-9_]*/g, "{}").replace(/\{[^}]*\}/g, "{}");
const withoutAlias = route => route.replace(/^(\S+ )\/(api|v1)(?=\/|$)/, "$1");
const mainRoutes = { hosted: registrations(mainNames, "hosted"), self_hosted: registrations(mainNames, "self_hosted") };
const factoryRoutes = registrations(names, "hosted"), mainSet = new Set(Object.values(mainRoutes).flat().map(canonical)), factorySet = new Set(factoryRoutes.map(canonical));
function family(route) {
  const path = withoutAlias(route).split(" ")[1];
  if (/\/library|\/cloud\/|\/ai\/(media-search|smart-library)|\/search|\/transfers/.test(path)) return "R5 Library/transfers/search";
  if (/\/billing|\/stripe/.test(path)) return "R7 payments/usage";
  if (/\/integrations|\/provider|\/connections|\/mail|\/oauth|\/social|\/realtime|\/mcp|\/webhooks|\/code\/github|\/figma|\/activepieces/.test(path)) return "R6 integrations/realtime";
  if (/\/tasks|\/calendar|\/agenda|\/roadmaps|\/roadmap-node-definitions/.test(path)) return "R3 Planner";
  if (/\/misty|\/ai|\/agent|\/runs|\/conversations|\/messages|\/nodes|\/workflow|\/studio/.test(path)) return "R4 Misty/agents/tools";
  if (/\/notes|\/drawings|\/journal/.test(path)) return "R5 Library/Journal";
  if (/\/deletion/.test(path)) return "R2 account/Space deletion";
  if (/^\/(livez|readyz|health|metrics|debug)/.test(path)) return "R10 operations";
  return "R8 other API/account/admin";
}
const go = (await readFile(join(root, "test/contract/http/app/routes.golden"), "utf8")).trim().split("\n");
const routes = go.map(route => ({ route, family: family(route),
  status: mainSet.has(canonical(route)) ? "main_registration_available" : factorySet.has(canonical(route)) ? "factory_not_composed" : "missing_native_registration",
  verifiedParity: false }));
const summary = Object.fromEntries([...new Set(routes.map(row => row.family))].sort().map(name => {
  const group = routes.filter(row => row.family === name), missing = group.filter(row => row.status === "missing_native_registration");
  return [name, { baselineAliases: group.length, mainRegistrations: group.filter(row => row.status === "main_registration_available").length,
    factoryOnly: group.filter(row => row.status === "factory_not_composed").length, missingAliases: missing.length,
    missingCanonicalOperations: new Set(missing.map(row => canonical(withoutAlias(row.route)))).size }];
}));
const rpcSet = new Set([...plannerRpcMethods, ...journalRpcMethods, ...connectionRpcMethods, ...mailRpcMethods, "spaces.get", "spaces.members.list"]);
const startup = await readFile(join(root, "internal/app/run.go"), "utf8");
const functions = new Map([...startup.matchAll(/^func (\w+)\([^]*?(?=^func |$(?![^]))/gm)].map(match => [match[1], match[0]]));
const workers = [...startup.matchAll(/WorkerFunc\(func\(ctx context\.Context\) \{ (.*?) \}\)/g)].map(match => {
  const name = match[1].split("(")[0], body = functions.get(name) ?? match[1];
  return { entry: name, source: "internal/app/run.go", currentOwner: "go_until_explicit_handover", verifiedParity: false,
    calls: [...new Set([...body.matchAll(/\b((?:server(?:\.\w+)?|service)\.\w+)\(/g)].map(call => call[1]))] };
});
const deviceSource = (await readFile(join(root, "internal/app/server_mount_spaces_routes.go"), "utf8")).split("func (s *Server) mountAgentsRoutes")[1];
const deviceRoutes = [...deviceSource.matchAll(/s\.Router\.(Get|Post|Put)\(prefix\+"(\/devices[^\"]*)"/g)].map(match => ({
  route: `${match[1].toUpperCase()} ${match[2]}`, aliases: ["", "/api", "/v1"], source: "internal/app/server_mount_spaces_routes.go",
  condition: match[2].includes("workflow-node-jobs") ? "MISTY_DEVICE_JOBS_ENABLED" : /pair|presence|peers|peer-ticket/.test(match[2]) ? "serverConnectedDevicesConfigured()" : "either device feature enabled",
  status: mainSet.has(canonical(`${match[1].toUpperCase()} ${match[2]}`)) ? "main_registration_available" : "missing_native_registration",
}));
const nativeWorkerNames = [...composition.text.matchAll(/startPollingWorker\(\{ name: "([^"]+)"/g)].map(match => match[1]);
const report = {
  description: "Go baseline compared with actual Hono factory registrations. Main means an optional dependency is supplied in main.ts with its feature configuration enabled, not live availability or verified behavior. Named HTTP SDK coverage is not whole-server parity.",
  baseline: "test/contract/http/app/routes.golden; TestRouteInventory checked separately. Conditional mounts excluded by that fixture remain in go-inventory.json and the explicit review list below.",
  rpc: { registered: Object.keys(mistyServerMethods).length, native: Object.keys(mistyServerMethods).filter(method => rpcSet.has(method)).length, missing: Object.keys(mistyServerMethods).filter(method => !rpcSet.has(method)) },
  nativeDependencies: { declared: [...names].sort(), presentInMain: [...names].filter(name => mainNames.has(name)).sort(), absentFromMain: [...names].filter(name => !mainNames.has(name)).sort() },
  summary, routes, nativeRoutes: mainRoutes,
  factoryOnlyRoutes: factoryRoutes.filter(route => !mainSet.has(canonical(route))),
  workers,
  nativeStartupWorkers: { source: "apps/api/src/main.ts", names: nativeWorkerNames,
    limitation: "Most workers require explicit feature flags and configuration. A matching worker name does not establish legacy job ownership handover or complete service behavior." },
  additionalStartup: [
    { entry: "Database.Start", source: "internal/platform/postgres/db.go", remaining: "Native boot must require complete migration history and an appropriate runtime role; no database worker is started here." },
    { entry: "Server.StartRealtime", source: "internal/app/server_mount_spaces_routes.go", remaining: "Realtime listener/hub and compatible HTTP/WebSocket delivery remain unported." },
    { entry: "AbuseGuard.StartRefreshLoop", source: "internal/app/server_mount_handlers.go", remaining: "Shared persisted abuse blocks and refresh behavior require an owner; current native local rate limits are not this feature." },
    { entry: "Metrics.StartSampling", source: "internal/app/run.go", remaining: "Existing domain gauges, protected metrics endpoint and shutdown integration require an owner." },
  ],
  conditionalMountReview: {
    devices: deviceRoutes,
    metrics: { route: "GET /metrics", source: "internal/app/server_mount_handlers.go", condition: "MISTY_METRICS_TOKEN configured", status: "missing_native_registration" },
    activepieces: { source: "internal/app/activepieces_proxy.go", condition: "MISTY_ACTIVEPIECES_PROXY_URL configured",
      paths: ["/activepieces", "/activepieces/*", "/.well-known/oauth-protected-resource/activepieces/*", "/.well-known/oauth-authorization-server/activepieces", "/.well-known/openid-configuration/activepieces"], methods: "chi.Handle all methods", status: "missing_native_proxy" },
    library: { source: "internal/app/server_mount_handlers.go and server_mount_drawing_routes.go", condition: "Library service exists; baseline already contains these routes", status: "see baseline rows; provider/store readiness still needs integration" },
    stripe: { source: "internal/app/server_mount_handlers.go", condition: "hosted; configured StripeWebhookPath", status: "isolated payments handler exists; legacy URL routing/handover remains R7/R10" },
    modes: "Hosted baseline includes Stripe; self-host registration uses a closed handler. Native mode-specific registration sets are recorded separately; route names alone do not verify these policies.",
  },
  commands: [
    { source: "cmd/misty-server/main.go", native: "apps/api/src/main.ts and apps/payments/src/main.ts", status: "incomplete_domains_and_assembly" },
    { source: "cmd/misty-admin/main.go", commands: ["bootstrap-token", "reset-password", "disable-account"], native: "apps/api/src/admin.ts", status: "implemented_pending_final_operations_check" },
    { source: "cmd/journal-collab-ticket/main.go", native: null, status: "missing_native_command_existing_signer_available" },
    { source: "cmd/smart-library-eval/main.go", native: null, status: "missing_native_evaluation_command_no_live_execution_authorized" },
  ],
};
await writeFile(join(root, "docs/migration/native-coverage.json"), JSON.stringify(report, null, 2) + "\n");
console.log(JSON.stringify({ rpc: report.rpc, absentFromMain: report.nativeDependencies.absentFromMain, summary, startupWorkers: workers.length }, null, 2));
