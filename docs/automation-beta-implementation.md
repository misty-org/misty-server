# Everyday automation implementation status

The accepted plan is **partially implemented**. This working tree contains the
shared contracts and substantial execution-safety work. It is not the completed
catalog/routines beta and is not a release-readiness declaration.

Go remains the control plane; TypeScript/Vercel remains the durable runtime.
Existing Activepieces flows and Space conversations remain intact. Embedded
Inbox/Social UI, login, profiles and platform selection belong to the concurrent
browser work. No release, deployment or seven-day pilot has been performed.

## Current delivery priority

Following the user's request to accelerate delivery, prioritize connected runtime
loop, harness and SDK implementation. Batch focused compilation/contract checks
after each connected slice. Repeated broad stress suites, complete catalog work
and the seven-day pilot are deferred; release requirements are unchanged.

## Implemented

### Shared SDK and browser contracts

- Capability, owner-qualified provider, target, version, execution, availability,
  evidence and typed outcome schemas in the public contracts package.
- SDK helpers for capability declarations, discovery, registration, invocation,
  result polling and cancellation. All eight public capability methods now have
  backend routes. Backend providers execute through the existing registry and
  durable runtime; browser/native/view execution still needs host integration.
- Semantic Inbox read/search/draft/send/manage and Social read-thread/search/
  draft-message/send-message contracts. The same definitions validate for Gmail,
  Outlook and a backend provider; this is schema conformance, not live compatibility.
- Native and SDK fill/select/scroll/bounded-key primitives. Fresh document and
  element references are consumed; outputs explicitly report attempted actions.
- Source-target, coverage, truncation and evidence requirements. Sends require a
  matching draft hash and recipient list; result contracts distinguish observed
  acceptance from recipient delivery.
- Public package snapshots refreshed in Misty, misty-apps and misty-server.
  Go RPC resolution now supports account methods and escaped provider identities.

### Verified independent SDK installations

- Added signed manifest verification, first-install publisher key pinning, and
  immutable app/provider/semantic capability versions. First-install trust is
  explicitly user review, not a certification of publisher identity or reputation.
- A separate trusted-account install/session/uninstall path accepts independent
  apps. Catalog identities cannot be replaced, and app bearers cannot install or
  grant authority. Failed installation checks roll back proposed permissions.
- Provider registration must match the current reviewed manifest and authenticated
  app owner. Downloaded apps cannot introduce native or server host adapters.
- Added registration, unregister, ordered availability reports, and paginated
  discovery constrained by both current grants and admission scopes. Target-bound
  discovery uses the exact authorized backend target and never falls back to a
  different account or implementation.
- New installation/session/registration admissions default off behind
  `MISTY_SDK_PROVIDERS_ENABLED`. Disabling admissions preserves lifecycle records.
- Account-scoped SDK RPC no longer incorrectly requires a Space membership.
  Space-bound operations still check current membership and matching Space.
- Fixed old app tokens surviving uninstall/reinstall or a version change.
  Version/consent changes and reinstall require fresh provider registration;
  rolling back an app version cannot automatically revive a retired registration.
- Details and request formats: [SDK provider installation](sdk-provider-installation.md).

### Backend target bindings and registry extension

- Added trusted controls for encrypted backend connections and explicit targets.
  Credential encryption binds user, app, connection and configuration revision.
  Endpoints are fixed HTTPS URLs; the shared outbound transport also checks DNS/IP
  destinations, disables proxies, and refuses redirects during execution.
- Target versions pin provider/version, installed app/version, Space, permitted
  capabilities, allowed calling apps and the private connection revision. Changing
  an endpoint or credential requires a new target revision before it can resolve.
- Resolution checks current installation grants, caller scope ceilings, explicit
  cross-app access, current Space membership, target revision and connection state.
  Revoked or stale targets cannot fall back to another target. Backend target
  resolution now works through the SDK RPC path.
- Corrected target Space IDs to support Misty's existing opaque `space_...` IDs;
  refreshed all public package snapshots and checked the packed consumer.
- Added a constructor in the existing tool registry for pinned provider/target
  actions, including full JSON Schema validation and minimum interactive approval
  for mutations/incidental effects. It preserves semantic identity separately from
  the model tool key and snapshots mutable registration metadata.
- Generated the reserved Inbox/Social contracts from the same packaged SDK data.
  An installed provider cannot weaken those schemas/effects or invent a new
  Misty-owned semantic version. SDK invocation runs now advertise their exact
  registered tool through MCP. Managed Misty conversational runs now discover
  their admission-pinned Space targets in this registry as well. Quick AI requests
  expose their account or Space target snapshot through the same registry. The
  actual packaged sample/model proof remains outstanding.

### Target-bound backend execution

- SDK admission pins provider/capability/target versions, input, adapter version,
  deadline and a stable effect ID. Metadata attaches to the existing AI invocation;
  admission and dispatch commit in the same transaction. Concurrent retries return
  the same run and effect, while changed-argument retries conflict.
- TypeScript executes the exact host-admitted capability through the shared MCP
  path, including its approval handling. It cannot select a replacement provider,
  change the input or allocate a fresh identity for a pending action.
- Backend execution rechecks current authority and target before dispatch. Uses
  the stored encrypted connection, fixed endpoint, bounded response/deadline,
  schema validation and an effect ID as the provider idempotency key.
- Mutations require trusted individual approval when an independent manifest
  declares scoped approval; a manifest cannot grant that approval itself. Approval waits and resumes use the existing
  approval table and durable delivery outbox; app bearers cannot approve themselves.
- Protected provider observations and replay data are stored separately from
  redacted history. Completion comes from the effect journal, not runtime prose.
  Missing effects fail; ambiguous writes remain uncertain and cannot be retried
  automatically. Completed runs reject further execution.
- `MISTY_SDK_EXECUTION_ENABLED` defaults off. Cancellation stops new dispatch and
  queues cancellation for a bound runtime, preserving confirmed/uncertain effects.
  Sign-in/intervention waits and explicit uncertain-effect reconciliation controls
  remain unfinished. Registered providers must explicitly report available before
  target resolution/execution; unavailable or authentication-required reports stop
  subsequent execution even when credentials and a target are configured.
- Installation generations now persist in app credentials and run authority.
  Revocation, version/consent changes and reinstall invalidate older principals,
  even if permissions are later restored. App result retrieval also checks the
  original generation and current target access. Old queued app runs that lack a
  generation fail closed; trusted user runs and stored history remain intact.

### Independent sample backend

- Added `misty-sdk/examples/habit-tracker`, a standalone Node/SQLite adapter,
  public SDK client, capability manifest and persistent Ed25519 signing command.
  It declares `habits.list`/`habits.record` without official catalog changes.
- The adapter commits habit data and the effect receipt together. Restart/replay
  preserves the original result; changed input/targets conflict. Listings include
  explicit pagination. Authenticated HTTP requests pin the execution and target.
- Six sample tests pass, covering restart/replay, pagination, validation, expired
  effects, app-scoped registration, signer continuity and HTTP authentication.
  The packed-consumer check also runs the sample outside the workspace against
  only the public package archives.
- Trusted installation/configuration instructions are included. The actual
  independent install → conversational agent → habit → Journal proof remains
  outstanding; a packaged adapter test is not that end-to-end proof.

### Conversational provider integration

- Managed Misty conversational run admission now snapshots explicitly Space-bound
  provider/capability/target versions in the same transaction as the run and job.
  Delegation inherits the parent's exact bindings. Later installation or target
  changes cannot expand old runs or replace their account/implementation. An
  unavailable but authorized target stays pinned and can recover readiness without
  replacing the target or creating a new run.
- The existing registry exposes these providers alongside Journal and other app
  tools. Target labels identify accounts; the runtime is instructed to resolve
  ambiguous accounts with the user. No habit-provider or official-app exception
  was added to the harness.
- Conversational SDK actions share the direct SDK outbound dispatcher, schema
  validation, encrypted effect journal and trusted approval mechanisms. Their
  deterministic effect identity depends on the admitted run and logical call,
  and replay restores the original evidence rather than regenerating it.
- SDK mutations always require individual approval, regardless of a conversational
  run's broad mode. Provider outcomes include observed evidence and partial status
  in the model-visible result.
- A prior unconfirmed effect prevents a replanner from proposing a replacement
  SDK action. The response retains the original uncertain effect ID. A transaction
  serializes the final check with effect admission, and dependent conversational
  writes (including Journal/MCP writes) are also blocked.
- Compatibility adapters retain AI invocation and older session identities. The
  new execution-state check distinguishes those identities from Space run IDs.
- Current limits: this integration covers managed Misty creator/conversation runs
  with explicit Space targets and quick AI requests with account/Space targets.
  Account-only targets are not implicit shared-Space authority. Assigned/custom-
  agent policy controls, unavailable-target presentation and durable authentication
  waits need completion.

### Quick AI capability execution and reviewable approvals

- Ordinary AI invocation admission now pins its authorized capabilities/targets in
  the run transaction. Existing binding metadata references either authoritative
  Space runs or AI invocations with database foreign keys. Linked agent runs inherit
  the invocation's exact versions and app principal instead of taking a fresh scope.
- Quick requests discover and execute SDK tools through the same registry and
  backend dispatcher as conversations, preserving full provider result evidence.
  `ai.write` alone grants no provider scope. Later permission grants cannot expand
  an old run; revocation also blocks linked child runs.
- SDK approval snapshots contain the validated execution/input, public target,
  declared effects and description. Encrypted snapshots commit atomically with
  their waits, survive restart and cannot change on retry. No approval is admitted
  without its review. Trusted controls retrieve it through
  `GET /v1/me/capability-approvals/{approvalID}`; app/runtime credentials and other
  users cannot read it. Endpoint credentials and continuation secrets are excluded.
- Quick SDK mutations use distinct durable approval waits and safe replay. A lost
  mutation reply retains the original uncertain effect ID; a new proposal cannot
  authorize a replacement effect. Completion checks see the unconfirmed effect.
- Trusted Activity controls now discover pending SDK approvals, load their exact
  protected reviews and approve/deny the individual action through its authoritative
  run. Account switching discards old requests/data. Expired, unreadable and already
  decided reviews cannot approve; a lost decision response requires a fresh review.
  Provider input renders as plain text, never HTML or instructions.
- A paginated pending-approval endpoint excludes decided, expired, cancelled and
  stale waits. Activity refreshes while the app is open, exposes further pages, and
  uses its existing attention/notification path for newly discovered approvals.
  Protected review contents are held in memory only and omitted from notifications.
- Desktop/phone browser fixtures verify layout, long and mixed-language input,
  overflow, touch targets and approval confirmation. These are API doubles, not
  live model or native-device proofs. Native macOS/iOS controls still need validation.
- Fixed Vite development resolution so excluded SDK contracts use their own Zod 4
  ESM dependency while host code retains Zod 3. The actual rendered browser fixture
  caught this startup failure; a dependency-resolution regression now covers it.
- Remaining approval work: inline Agents/run presentation, durable approval events,
  broader notification recovery across closed clients, and standing grants.

### Authority, effects and recovery

- Authenticated app identity is persisted at invocation/run admission and inherited
  by delegated runs; caller-supplied authority cannot replace it.
- Tool execution checks admission scopes and current installed-app grants.
  Unscoped implicit memory/conversation retrieval is omitted for app principals.
  App bearers cannot approve commands through trusted approval routes.
- Approving one command no longer changes the entire run to full access.
  Approval IDs, command hashes and argument hashes remain bound to the action.
- Invocation admission and dispatch intent commit together. A leased outbox
  recovers invocation starts, approval/device resumes and runtime cancellation.
- Device waits identify their exact scope/capability and hook. A stale resume
  acknowledgement cannot erase a later wait. Expired waits resume as unavailable.
- Repeated approval/device waits are processed in a bounded loop. Missing results
  and contradictory waits cannot become success.
- Stable logical call IDs identify effects. Changed arguments conflict. Concurrent
  workers cannot execute an already claimed effect; confirmed results replay.
- Ambiguous mutation failures remain uncertain. Encrypted MCP replay data is
  separate from redacted audit data. Legacy workflow claims no longer replay
  failed/started rows as successful results or blindly retry mutations.
- Stale heartbeats reconcile the pinned runtime identity rather than clearing it
  and restarting the original prompt. An exited runtime without a confirmed
  completion becomes a visible failure requiring reconciliation.
- Invocation event sequences and callback receipts are allocated in the database.
  Committed events are restored across process boundaries before streaming.
- Completion checks unconfirmed effects; incomplete runtime responses are no
  longer projected as successful invocations. The harness stops on uncertainty.

### Registry-driven harness

- Removed the duplicate built-in native tool schemas and name map from the TS
  harness. Its model tools now come from the existing Go registry through MCP.
- Initial model exposure is bounded to 31 relevant capabilities plus discovery.
  Paginated discovery keeps every admitted capability reachable.
- A missing registry fails explicitly. Keep the previous runtime available during
  a coordinated deployment; this version does not synthesize fallback schemas.
- New durable-runtime AI/agent admissions persist a 20-turn model budget. Go claims each stable
  `model:*` checkpoint before the runtime starts its model step. A row lock
  serializes competing claims; retries of the same runtime/node reuse the claim.
  Exhaustion returns a non-retryable, explicit budget outcome and retains completed
  work. The runtime's own 20-step stop remains as a second boundary.
- Claims recheck current user/Space/app authority and pinned runtime identity.
  Waiting, stopped or substituted runtimes cannot claim a new turn. Approval waits
  and process recovery cannot reset consumed turns. Limits apply even without a
  hosted billing meter. Older admissions retain their pinned worker's existing
  behavior while draining; migration does not guess missing historical counts.
- Aborted streams cannot report success by returning an earlier completed model
  step. The harness records the adapter's abort callback and requires a finished
  model response; truncation, unfinished tool turns and provider errors remain
  incomplete. Confirmed effects and reported usage remain available for recovery.
- New durable-runtime admissions now persist a 30-minute execution clock in Go.
  Operation admission starts it under the authoritative run row lock. Every wait
  or terminal state change pauses/debits it in the same database transaction.
  Queued starts and undelivered resumes do not consume time. An unacknowledged
  running interval conservatively remains charged across a server restart; the
  system cannot assume a remote worker stopped merely because contact was lost.
- Model admission and tool handlers check the clock, current app/user/Space
  authority and pinned runtime. Tool deadlines are checked inside the effect
  journal's handler so expired budgets still permit confirmed-result replay.
  Backend execution envelopes carry the shorter run/transport deadline. The habit
  sample excludes that changeable deadline from new effect fingerprints while
  retaining exact-envelope compatibility with its earlier stored receipts.
- The runtime serializes each tool's complete lifecycle, including durable waits
  and end checkpoints. A second tool proposed in the same model response cannot
  overwrite the first tool's approval/device wait. Concurrent arbitrary work
  within a run is not supported by this single-wait coordinator.
- The Misty coordinator now makes one model turn per pinned WorkflowAgent stream
  call, carries the complete tool transcript forward and fetches the Go budget
  before the next turn. Each model call receives the smaller remaining duration
  and absolute deadline. The aggregate 20-turn cap and stable model checkpoint IDs
  remain across calls. A second model call inside one stream is rejected. This
  removes the wait-consuming absolute timer for new admissions without patching
  the upstream library; older admissions retain their absolute timeout policy.
- Added the Misty-owned lifecycle and execution interfaces, with a single Vercel
  adapter for start/status/resume/cancel and shared checkpoint/completion types.
  Runtime start accepts an explicit adapter version and persists it in workflow
  input; an unsupported version is rejected rather than substituted. Both Go run
  tables now pin the adapter at admission and retain worker/callback endpoints
  from first delivery. Actual worker retention, rollout flags and deployment
  validation remain operational release work.
- Tool transport responses normalize into typed success/failure/approval/device/
  intervention/uncertain outcomes. Missing or contradictory responses and invalid
  approval waits fail closed. The outer loop stops on uncertainty or unresolved
  user intervention; durable sign-in/intervention resume controls remain open.
- Increased per-turn output allowance to 8,192 tokens for complete artifacts and
  tool arguments; the durable 20-turn and active-time limits still apply.

### Device execution controls

- Migration `20261228000000_device_execution_controls.sql` adds protocol version,
  pinned run identity/capability, bounded deadline, execution-start acknowledgement,
  cancellation request and uncertain state to device work. Parent terminal changes
  stop new starts. Old hosts drain old jobs and cannot claim protocol-2 work.
- Claim, begin and renewal recheck the current run, app grants, Space membership,
  exact context and trusted device. Only a provably unstarted lease is redelivered;
  begun work becomes uncertain after a lost lease. Original device receipts can
  reconcile late results, and a conflicting callback cannot overwrite completion.
- Browser polling stops the durable job when transport or deadline expires. A
  possibly executed operation returns uncertainty through the existing effect
  journal rather than opening another automatic planning attempt.
- Desktop worker now acknowledges begin, bounds work by its last acknowledged
  lease, fences stop/restart loops, and separates execution from result delivery.
  Three attempts deliver the same receipt; they never repeat the native operation.
  Browser grants are unique to each job and limited to its requested operation.
- Native desktop browser controls enforce per-job deadlines/leases, interruption,
  duplicate-start refusal and cancellation tombstones. Grants are revoked when
  work stops. Already dispatched website effects cannot be undone; interrupted
  browser work is conservatively uncertain. Full sleep/reconnect and signed-in
  compatibility validation remain open. Native file preparation is read-only and
  may finish locally after observation stops; it has no interruptible parser yet.
- Public SDK adds `discoverAll`, `waitForResult` and `invokeAndWait`. Discovery
  detects cursor loops; polling returns explicit waits without approving them.
  Invoking with an abort signal requests cancellation, including the race where
  cancellation overtakes admission. Observation timeout never claims cancellation.
  SDK archives were rebuilt and synchronized into host/apps and server contracts.

Complete usage accounting, delegated budgets, browser provider target integration,
real deployed pause/restart recovery and durable authentication waits remain open.

### Browser driver and registry schema integration

- The agent registry now exposes `browser.interact` using the public SDK's exact
  fill/select/scroll/key schema, generated into the Go contract snapshot. Attached
  browser contexts and agent-owned research views can advertise the capability.
  This is a general browser primitive, not a complete semantic Inbox/Social driver.
- Registry schemas compile at registration with the same JSON Schema engine used
  by independent providers. Built-in tools now enforce unions, constants, numeric
  bounds and field constraints before entering the effect middleware. Remote
  schema references are rejected. The legacy graph executor was not extended.
- Native inspections return a document nonce. The new agent interaction requires
  that nonce and the current inspected element where applicable. A newer inspection
  invalidates the older action, including page-level scrolling. Existing SDK host
  views retain their own document-reference validation and remain compatible.
- Snapshot truncation now covers omitted actionable elements as well as text.
  Typing descriptions acknowledge possible autosave/website event effects rather
  than promising that arbitrary website controls cannot submit data.
- Click/interact descriptors require interactive approval. Creator agent runs use
  their existing durable approval path. Quick AI's generic built-in-tool path still
  needs approval delivery and trusted review controls; until then these interactions
  are refused instead of bypassing approval. This is a known usability gap to close
  in the next runtime integration slice, not claimed complete browser support.
- Latest public snapshots include the concurrent shared-source SDK update (88
  server methods). Its UI/source changes were preserved; only packages and generated
  route/schema artifacts were synchronized.

## Validation

- Latest SDK batch: 72 tests passed, build/typecheck passed, isolated packed consumer passed.
- TypeScript runtime: 54 tests passed, typecheck and production build passed. Includes pinned-action
  recovery identity, refusal to substitute unavailable providers and terminal
  classification of durable model-budget exhaustion. Workflow orchestration tests
  verify that abort callbacks, response limits, unfinished tool calls, empty model
  results and provider finish failures cannot publish successful completion.
  Tool lifecycle tests cover waiting, duplicate in-flight IDs and recovery after
  failed start/end checkpoints without overlapping tool execution.
  Tests using the actual pinned adapter with a deterministic model verify one
  model call per stream, complete transcript reuse with one system instruction,
  serialized tool execution, a simulated six-hour wait plus a real expired model
  timer during a tool wait, and interruption of an in-flight model stream.
  Coordinator fixtures verify refreshed deadlines, global model node IDs, the
  aggregate 20-turn cap, exhausted-budget refusal and retained earlier usage.
- Host: typecheck passed; browser permission/backend/interaction fixtures passed.
- macOS: `cargo check --lib` passed; live device lifecycle remains unvalidated.
- Full fresh-database regression after applying all 179 additive migrations:
  Go internal tests passed; PostgreSQL contracts passed (90.6 seconds), HTTP API
  contracts passed (29.9 seconds), HTTP app/route contracts passed (2.6 seconds).
  The two earlier SDK route contract failures remain resolved. Focused quick-AI,
  provider, target and approval-review contracts also passed (PostgreSQL 11.2
  seconds; HTTP API 8.1 seconds).
- New quick-AI fixtures verify account discovery, exact target/effect identity,
  approval and confirmed replay, repeated waits, and retained uncertainty after a
  lost write. Database tests deny AI-only provider escalation, pin later grants
  out of old requests, preserve AI-to-agent delegation authority/versions, and
  reject linked execution after revocation.
- Approval-review fixtures restore exact inputs/targets/effects using a fresh
  service instance. They deny other users and app/runtime tokens, verify protected
  storage and no credential leakage, reject changed review payloads, preserve the
  original ciphertext on retry, and roll back waits lacking review data.
- Follow-up pending-approval discovery contracts passed on all 177 migrations:
  PostgreSQL 12.6 seconds and HTTP API 9.1 seconds. Fixtures cover pagination and
  exclusion of decided/cancelled/expired waits, plus current owner-only discovery.
  All 37 host approval/activity tests (nine files), typecheck and two SDK/Vite
  resolution/reload tests passed. The dedicated browser renderer checks the shipped
  component at desktop and phone widths with API doubles; screenshots are in
  `/tmp/misty-capability-approval-preview`. Native device validation remains open.
- Model-budget concurrency/recovery contracts passed on 178 migrations. For both
  Space runs and AI invocations, 32 competing model callbacks admit exactly 20;
  duplicate callbacks preserve the count. Tests reject substituted runtimes,
  cross-user access and revoked app grants, and retain counts across approval waits.
  The signed runtime endpoint rejects callback 21 with a non-retryable limit code
  even without a billing meter. The earlier full-run OAuth fixture failure is
  superseded by this pass: its localhost case now explicitly clears the developer's
  public API URL setting, and a second assertion verifies configured URL precedence
  over a forwarded host. Production OAuth behavior was unchanged.
- Execution-clock contracts on 179 migrations verify a stable deadline across
  competing/retried admissions, repeated approval/device waits, rollback of wait
  transitions, terminal stopping, legacy version pinning, revoked app authority,
  exhausted-run model refusal and authenticated budget inspection. Cross-app
  HTTP/MCP fixtures replay both SDK and Journal effects after exhaustion, reject
  a fresh note without writing it, and preserve uncertain outcomes. The six
  standalone habit-adapter tests also pass with changed-deadline replay.
  Follow-up clock/provider contracts passed after the final deadline propagation
  changes: PostgreSQL 16.0 seconds and HTTP API 14.0 seconds. The provider fixture
  verifies that the transmitted deadline matches the bounded request context.
- SDK HTTP-to-MCP fixture confirms private connection use, exact provider/target/
  effect identity, replay without a second backend call, protected result delivery,
  and failure when a runtime reports success without executing the action.
  Follow-up SDK/MCP tests pass for trusted approval, rejection of self-approval,
  late calls after completion, and lost mutation replies: one backend attempt and
  a final uncertain outcome, with no automatic retry.
- Database fixtures cover concurrent admission, atomic dispatch, approval identity,
  self-approval denial, cancellation, and revocation followed by restored grants.
  Provider lifecycle/target, effect-recovery and shared outbound transport security
  suites remain part of regression coverage.
- Contract snapshots and 88 Go SDK method routes agree. Route inventory includes
  the SDK invocation, trusted approval and protected review endpoints.
- `scripts/test-automation-beta.sh` resolves the database-test permission blocker
  by provisioning a loopback-only, disposable pgvector/PostgreSQL 16 database,
  applying migrations, checking an ordinary RLS role and cleaning its own data.
  The earlier disk-exhausted and incomplete-route runs are superseded by the full
  fresh-database pass above. No developer/shared database was reset.

Run `./scripts/test-automation-beta.sh` for the full Go validation. Do not run the
suite against a developer or shared database; its reset is intentionally destructive.

### Focused validation for the accelerated core batch

- Runtime: typecheck, 12 focused outcome/continuation/model-budget tests and
  production build passed. Existing deterministic real-library model-loop tests
  were not rerun as part of this batch.
- Device controls: two focused PostgreSQL contracts passed in the disposable
  database, including old-host refusal, required begin, fresh lease tokens,
  no repeat after execution start, late receipt replay and cancellation outcomes.
  The script also runs internal Go checks; no new stress run was requested.
- Host: typecheck, six worker tests and macOS `cargo check --lib` passed.
- SDK: 72 tests and isolated packed-consumer verification passed; host and apps
  use refreshed immutable package archives. No publication or deployment occurred.
- Logs: `/tmp/misty-core-sdk-sync.log`, `/tmp/misty-core-device-contracts.log`,
  `/tmp/misty-core-host-types-final.log`, `/tmp/misty-core-worker-tests-final.log`,
  `/tmp/misty-core-native-check.log`. Runtime build/test results are recorded in
  this task's tool output.

### Browser registry validation batch

- Focused disposable-database contracts passed for browser discovery and device
  execution controls; internal Go checks passed, including union/bounds rejection
  before effects and the generated browser schema.
- Eight deterministic DOM tests passed, including stale document rejection,
  one-use interaction, selection retention and element-list truncation.
- macOS native compilation passed. These fixtures do not establish real website
  compatibility or live delivery verification.
- Public SDK checks (72 tests) and packed-consumer validation passed during the
  shared-source snapshot refresh. Logs: `/tmp/misty-browser-registry-contracts.log`,
  `/tmp/misty-browser-registry-dom.log`, `/tmp/misty-browser-registry-native.log`,
  `/tmp/misty-browser-registry-sdk-sync.log`.

### Quick-AI browser approval and runtime deployment bindings

- Fixed a model-facing registry gap: authorized browser tools were absent from
  quick-AI MCP discovery. MCP now uses the existing browser descriptors and adds
  the attached opaque targets; it does not create a second capability catalog.
- Browser click and bounded interaction now pause for trusted individual review.
  The protected proposal binds the original run/call, input, attached context,
  device, scope, page and control. Retries reuse that review, recheck authority,
  and replay confirmed effects through the existing effect journal.
- Activity renders page/control/content in plain text, with approve/deny controls.
  SDK and browser reviews share encrypted storage, trusted owner checks, expiry
  and the durable approval-resume outbox. The compatibility HTTP tool route uses
  this same browser path. No website text or app bearer can approve an action.
- Migration `20270119000000_agent_runtime_pins.sql` pins the beta adapter version
  on both authoritative run tables at admission. Before outbound delivery, Go
  persists immutable worker and callback endpoints. Starts, approval/device
  resumes, status reconciliation and cancellation reuse them after configuration
  changes. Unknown adapter versions fail rather than falling back.
- Existing pre-migration runs acquire their endpoint on the first subsequent
  delivery: deploy the migration while configuration still points to their worker.
  Keep that worker and compatible control secrets available through drain and
  rollback. Routing cannot recover an already-deleted worker deployment.
- Focused database fixtures passed for immutable deployment binding and a real
  MCP browser inspect → approval → click/fill → confirmed-replay sequence, including
  self/cross-user review denial, changed-input refusal and no unapproved dispatch.
  The browser results are deterministic device receipts, not real Gmail/Outlook
  interaction or proof of message delivery. Logs:
  `/tmp/misty-browser-runtime-batch-final.log`.
- Targeted internal Go packages, host typecheck and seven approval UI/API tests
  passed. Desktop/mobile browser previews passed layout and approval checks.
  The full internal suite encountered an unrelated in-progress provider-source
  Jira fixture failure; no full-suite success is claimed. The disposable runner
  accepts `MISTY_AUTOMATION_SKIP_INTERNAL=1` for focused contracts without repeating
  that suite. Its default still runs internal checks.
- Quick-AI authentication and intervention waits remain to be connected. Device
  waits are implemented in the recovery batch below. The browser primitives still require semantic providers, verified
  account bindings, session recovery and real-service validation.

### Quick-AI device waits and safe recovery

- Migration `20270120000000_ai_device_waits.sql` adds a durable `awaiting_device`
  state and exact wait bindings to existing AI invocations. Each wait retains its
  runtime, hook, call, argument hash, browser scope and attached context identity.
  Another call or target cannot replace a pending wait.
- Quick-AI MCP and compatibility tool routes return the existing typed device
  outcome. The TypeScript continuation loop resumes the same logical action,
  preserving the original browser approval and effect journal identity through
  repeated device waits. No original-prompt restart is introduced.
- Ready/expired waits are queued through the durable continuation outbox. Device
  availability is recomputed when queuing; current authority and target access are
  rechecked before an affirmative delivery and again before execution. Late
  acknowledgements cannot clear a newer wait. Expired waits resume negatively;
  canceled invocations cannot resume.
- Wait status and resume status events commit with their state transitions and
  use the database event sequence. Host sessions recognize `awaiting_device` and
  show the reason. Durable waits pause the existing active-execution clock.
- An unstarted protocol-2 browser job can be rearmed under its original job ID,
  after revalidating the run/device/context and clearing its old lease token.
  Recovery and delivery attempts are bounded. Jobs that began execution or have
  uncertain outcomes cannot be rearmed.
- Focused disposable-database contracts passed for approval → offline → reconnect
  → offline → reconnect → the same browser action; stale resume acknowledgements,
  expiry, cancellation, same-job recovery and started-action refusal. Logs:
  `/tmp/misty-device-waits-contracts-final.log`. Host typecheck and targeted
  internal Go checks are recorded in `/tmp/misty-device-waits-host-types-final.log`
  and `/tmp/misty-device-waits-core.log`.
- These are deterministic control-plane/device-receipt fixtures. Real macOS
  sleep/reconnect, browser closure/reopen, sign-in/MFA and account confirmation
  still require native/provider integration and compatibility validation.

### Quick-AI runtime liveness and recorded-result recovery

- Migration `20270121000000_ai_runtime_observations.sql` records runtime
  observations. Stale standalone invocations queue coalesced status requests
  through the durable outbox and query their pinned worker without restarting
  the original prompt.
- Live observations preserve durable waits. Stale terminal observations cannot
  overwrite a newer callback. An engine completion without a committed result
  produces a reconciliation-required failure, preserving history and effects.
- SDK recovery restores an already recorded provider result without calling the
  provider again. Focused disposable-database contracts and internal packages
  passed: `/tmp/misty-runtime-liveness-sdk-final.log` and
  `/tmp/misty-runtime-liveness-core.log`.
- Start submission is now protected by the durable receipt batch below.

### Exclusive runtime starts and stopping dependent actions

- Migration `20270122010000_agent_runtime_start_receipts.sql` adds service-only
  start receipts. A signed worker may claim an admitted run only with its pinned
  adapter and callback endpoint. Concurrent workers serialize through the run;
  only the first receives a submission token.
- The Vercel harness records the returned engine identity before acknowledging
  start. Repeated deliveries recover that receipt or the runtime identity already
  committed by activation, without submitting another engine run.
- Claims never expire into permission to resubmit. The installed engine API lacks
  a public start idempotency key, so a crash between claiming and submitting can
  leave an unconfirmed start. Existing bounded delivery eventually reports that
  state clearly. This chooses no duplicate submission over automatic recovery
  where there is no proof; it does not promise exactly-once engine admission.
- Deployment order: apply the receipt migration and signed control-plane route
  before upgrading the harness. Keep pinned prior workers available. This is an
  internal transport addition; no public SDK package change is required.
- The serial tool lifecycle now stops queued calls immediately after a failed,
  denied, intervention-required or uncertain action, or an unconfirmed checkpoint.
  This also covers several calls proposed in the same model response, before the
  outer model-loop stop condition runs. Completion retains partial work and never
  treats blocked dependent calls as successful effects.
- Focused runtime checks passed (21 tests across receipt, lifecycle and workflow
  completion fixtures), plus TypeScript checking. Disposable database contracts
  passed for competing workers, immutable receipts, activation-based recovery,
  canceled admissions and signed/pinned route access. Logs:
  `/tmp/misty-start-batch-runtime.log`, `/tmp/misty-start-batch-types.log`, and
  `/tmp/misty-start-batch-contracts.log`. Route inventory initially differed due
  to concurrent provider-source route additions; the reviewed inventory now
  includes those existing routes and the new receipt route. Its focused check
  passed in `/tmp/misty-start-batch-routes.log`.
- A final focused loop check also covers stopping before another model turn
  after a single denied action: `/tmp/misty-start-batch-loop-final.log`.

### Quick-AI user intervention and Activity controls

- Migration `20270123000000_ai_intervention_waits.sql` adds explicit
  `awaiting_intervention` state and durable waits bound to the original runtime,
  logical call, argument digest, attached context, device and opaque browser scope.
  Waits expire within 24 hours and pause the active-execution clock.
- `browser.request_user_action` is generated through the existing tool registry
  for quick AI browser targets that already grant inspection. It requests sign-in,
  an account check, a challenge, opening the target or review before an action.
  It does not dispatch a browser mutation or retry an uncertain send.
- Only trusted user controls can decide a wait. Cross-user decisions and app or
  runtime self-approval are denied. The decision and resume outbox commit together;
  target access and current authority are checked before delivery and call replay.
  Consumed wait receipts prevent replaying an old wake signal after the call advances.
- The harness handles repeated intervention outcomes through its existing opaque
  wake hook. Tokens advertise support through signed MCP capability negotiation;
  older pinned workers are not offered the new wait tool. Contradictory wait and
  completion envelopes fail explicitly.
- Activity now lists browser requests with the attached target label, reason,
  expiry, continue and stop controls. Quick-AI sessions retain the waiting state.
  A readiness response requires inspecting the original target again; it is not
  proof of account identity, sign-in success, or delivery of any message.
- Focused database, runtime and host type checks passed. Direct MCP discovery,
  wait admission, trusted HTTP decision and original-call replay passed. Desktop
  and mobile previews passed overflow, touch-target and response-confirmation
  checks. Evidence: `/tmp/misty-intervention-contracts.log`,
  `/tmp/misty-intervention-runtime.log`, `/tmp/misty-intervention-host-types.log`,
  `/tmp/misty-intervention-mcp-final.log`, `/tmp/misty-intervention-render.log`.
- The final signed-negotiation compatibility extension compiled, but its added
  old-worker discovery assertion could not run after Docker Desktop became
  unavailable. `/tmp/misty-intervention-mcp-negotiated.log` records that external
  failure. The earlier direct MCP proof passed before negotiation was added;
  rerun the focused negotiated proof when the disposable database is available.
  Final compile/type evidence: `/tmp/misty-intervention-core-final.log` and
  `/tmp/misty-intervention-runtime-types-final.log`.
- Browser changes still need automatic authentication/challenge detection,
  verified account bindings, profile-safe reopen and actual native compatibility
  validation. Custom-agent/Space-run intervention waits and global Activity
  notifications outside the Activity page remain to be connected.
- Concurrent work had chosen the same migration number as start receipts.
  The unpublished receipt migration is now `20270122010000`; the disposable test
  runner rejects duplicate migration numbers before applying migrations.

### Native browser profile observations and sign-in boundaries

- Browser inspection now includes an optional public SDK `target` observation:
  original scope, native profile digest, provider, actual origin and observation
  time. It explicitly reports `accountIdentity: unverified`; authentication is
  either `required` for a recognized sign-in location or `unknown`.
- Native classification uses the actual webview URL and the shared provider
  registry, with bounded same-site login recipes for selected Social platforms.
  Ordinary service URLs never establish a signed-in account. Provider OAuth
  callback pages are also treated as sensitive intervention surfaces.
- Known login/callback inspection returns a sanitized URL without query or
  fragment, a fixed explanation and no page text or interactive controls. Native
  click, type, interaction and provider-request operations refuse these surfaces.
  Normal inspection uses the host-observed URL and rejects navigation during
  snapshot collection. No cookie or browser credential export was added.
- A live native webview cannot silently change its profile metadata. Switching
  profiles requires closing/reopening through the owning browser workstream.
- Quick-AI browser instructions connect the observation to the implemented
  user-action wait, followed by fresh inspection of the original scope. This is
  not automatic account verification or reconciliation of an uncertain send.
- Public SDK checks passed (77 tests and isolated packed-consumer verification).
  Host/app archives and the server contracts snapshot were refreshed together.
  Host typecheck and macOS native compilation passed; four native provider-policy
  tests passed using the actual Rust module. Evidence:
  `/tmp/misty-browser-observation-sdk.log`,
  `/tmp/misty-browser-observation-server-sync.log`,
  `/tmp/misty-browser-observation-host-types.log`,
  `/tmp/misty-browser-observation-native-final.log`,
  `/tmp/misty-browser-observation-policy.log`.
- Detection covers known URLs, not every site's inline login or MFA experience.
  Real signed-in service validation, verified account bindings, origin-scoped
  target grants, profile-safe reopen, and Inbox/Social semantic execution remain.

## Browser binding storage and global intervention notifications

- Browser providers can now be bound through trusted target controls to an owned,
  nonrevoked device, profile digest, intended account UUID, allowed exact origins
  and optional SDK browser context. Origins cannot exceed the installed manifest.
  Immutable revisions preserve prior profiles and accounts. Migration
  `20270124000000_sdk_browser_target_bindings.sql` keeps backend connection fields
  nullable only for browser targets and preserves history on rollback.
- Trusted `GET /me/sdk-targets` (also `/api` and `/v1`) provides paginated target
  configuration, including disabled/unavailable bindings needed for review. It is
  not app RPC discovery and does not grant third-party apps user-control access.
- Public configuration/binding/inventory schemas match the backend. Capability
  device identities now accept actual `device_<uuid>` records as well as legacy
  UUIDs; previously the schema would have rejected actual registered Macs.
  Updated SDK archives are installed in host/apps and the server snapshot is synced.
- Browser bindings still fail clearly at execution. They cannot inherit the
  backend adapter at admission or dispatch, even after a provider reports itself
  available. The stored intended account is not verified website identity.
- Pending browser sign-in/review waits now feed global Activity attention and
  existing preference-aware native notifications on desktop/mobile. The global
  bridge and Activity controls share one account-scoped store and poller. Old GET
  responses cannot restore a decided wait; stale-account decisions/responses are
  discarded. Website content and model-authored reasons stay out of native
  notification text. Existing run events retain wait history after it clears.
- Validation: SDK check (78 tests), isolated packed-consumer verification, focused
  Go capability/router tests, host typecheck, and six focused host state/notification
  tests passed. The shipped browser-request controls also passed a desktop/mobile
  render check (layout, touch targets and saved-response confirmation); evidence
  `/tmp/misty-intervention-global-render.log`. New database target lifecycle/listing tests compile but have not
  run: Docker Desktop was unavailable at the prior database attempt. No claim of
  migration/integration validation for this batch. Evidence:
  `/tmp/misty-browser-bindings-sdk.log`, `/tmp/misty-browser-bindings-go.log`,
  `/tmp/misty-browser-bindings-compile.log`,
  `/tmp/misty-browser-bindings-server-sync.log`,
  `/tmp/misty-intervention-global-types.log`,
  `/tmp/misty-intervention-global-tests.log`.

## Space agent intervention waits

- Extended the existing intervention journal to Space/assigned-agent runs using
  additive migration `20270125000000_space_intervention_waits.sql`. Existing
  quick-AI wait identities remain intact. The database enforces one pending wait
  per run and one identity per logical call; wait creation/resume, run state,
  events and continuation outbox writes commit together.
- The negotiated MCP runtime exposes `browser.request_user_action` only with an
  attached browser read grant and current app/agent policy. Older workers do not
  see it, and the legacy tool endpoint cannot create unsupported waits. A lost
  response can retrieve the same pending wait; other tools remain blocked while
  the run is paused. An old completed wait cannot bypass a newer pending wait.
- Trusted Activity controls and global notifications now use the same journal for
  quick-AI and Space runs. User readiness rechecks the original context, device,
  membership, app authority and current agent policy before waking the pinned
  runtime. Readiness does not prove account identity or a browser effect.
- Waiting runs stay busy in scheduling and run summaries. Creator cancellation,
  account/agent teardown, active-work counts, and stale-runtime observation now
  recognize intervention waits. The existing execution clock pauses transactionally
  on the new state. Host/Agents types and member labels recognize “Waiting for you.”
- Focused Go runtime gates passed, host/Apps typechecking passed, and database/MCP
  integration tests compile. The integration tests cover repeated waits, expiry,
  revocation, trusted decisions, cancellation, pending-call replay and old-worker
  negotiation. They have not executed: the Docker availability probe hung and was
  interrupted rather than blocking implementation. Evidence:
  `/tmp/misty-space-intervention-core.log`,
  `/tmp/misty-space-intervention-runtime-gates.log`,
  `/tmp/misty-space-intervention-compile-final.log`,
  `/tmp/misty-space-intervention-host-types.log`.
- Required follow-up validation: apply the two new target/wait migrations in the
  disposable test environment and run the focused SDK target, quick-AI/Space wait,
  MCP and route-contract suites together. Real sign-in, account verification and
  device sleep/reconnect remain integration/pilot work, not proven compatibility.

## Saved routine contracts, drafts and manual execution

- Published bounded routine definition/execution/report schemas and the SDK's
  `defineMistyRoutine` validator. Go imports generated structural schemas and
  SDK-evaluated conformance fixtures; ordering, references, timezone and aggregate
  model-budget refinements are enforced in both languages.
- Added immutable owner-scoped drafts, expected-version writes, lost-response
  save replay, historical reads and paginated summaries. Draft controls are trusted
  user routes, not third-party app authority or routine enablement.
- Added a TypeScript durable routine coordinator for ordered capability steps,
  explicit references/conditions and partial/failure/uncertainty handling. It uses
  existing durable tool calls and the Misty harness. Bounded agent ports are now
  connected through signed Go controls; timed waits now use the same durable path.
- Added gated manual admission of backend capability routines using one existing
  AI invocation, exact provider/target pins, stable Go-issued call IDs and the same
  dispatch outbox in one transaction. Repeat requests preserve identity. Overlapping
  runs and a prior uncertain routine outcome block another admission.
- The backend independently derives each allowed step/input from confirmed
  protected receipts, verifies final outcomes against the effect journal and
  persists sanitized progress. Cancellation and stopped-runtime reconciliation
  reuse the existing outbox and worker. Cancellation blocks further effect claims;
  completing recovery after revoked membership does not restore execution authority.
- Old workers must negotiate routine protocol 1. Public host/app SDK packages and
  server/runtime snapshots are synchronized. Focused runtime tests (11), SDK checks
  (84), Go contract/evidence tests and host/Apps typechecking passed. Database
  integration tests compile but remain unrun; no Docker restart or shared database
  was used. Evidence: `/tmp/misty-routine-admission-focused.log`,
  `/tmp/misty-routine-final-core.log`, `/tmp/misty-routine-manual-sdk.log`,
  `/tmp/misty-routine-manual-host-types.log`; runtime check output is in this task.
- See `docs/routines-beta.md` for contracts, routes, deployment flags and limits.
  Apply draft/run migrations before the backend and deploy a compatible runtime
  before enabling admissions. No enablement UI, standing grants, automatic triggers,
  live database validation or rollout readiness is claimed.

## Bounded routine agent runtime

- Implemented a bounded WorkflowAgent loop for an individual routine agent step:
  exact pinned tools, one model call per turn, complete transcript continuation,
  refreshed active-time budget, metadata-only usage receipts and a 40-call cap.
  Partial/failure/uncertainty and interruption stop queued actions.
- Added public agent-step execution bindings with exact action-set validation and
  a unique admitted call namespace. The coordinator preflights every agent tool
  before executing the routine. Model call identities remain inside that namespace.
- Added the runtime's Go-control adapter: explicit step opening, protected result
  replay and validated completion acknowledgement. It never treats local model
  output alone as a completed routine step.
- Connected Go admission, encrypted step checkpoints, immutable call namespaces
  and model pins. The signed runtime endpoint opens the current step, admits model
  turns, records per-node usage, verifies output/effects and acknowledges completion.
  The actual workflow now supplies these ports to the coordinator.
- Tool admission checks the current agent action set and stable call/input mapping.
  Shared effect claims recheck closed checkpoints under the run lock. Model claims
  enforce step/run limits and sequential usage receipts; new calls cannot enter
  during approval waits. Protected completed outputs feed dependent steps.
- Completion publication counts each agent's actual SDK effects. Cancellation and
  stopped-worker recovery seal incomplete agent checkpoints without replaying a
  prompt; confirmed/partial/uncertain effects retain their actual state.
- Agent steps require the separate default-off `MISTY_ROUTINE_AGENTS_ENABLED` flag
  and worker negotiation of routine-agent protocol 1. Apply migration
  `20270128000000_routine_agent_steps.sql` before the updated backend. No flag,
  deployed worker, production migration or standing grant was enabled here.
- SDK package checks previously passed (85 tests), and its public host/app/server
  snapshots remain current. This connected slice passed Go evidence tests, runtime
  typechecking, 25 focused runtime tests and the actual Nitro/Workflow build.
  Database/route contract packages compile; their integration tests remain unrun
  because the disposable database environment is unavailable. Evidence:
  `/tmp/misty-routine-agent-wired-go.log`,
  `/tmp/misty-routine-agent-wired-runtime.log`,
  `/tmp/misty-routine-agent-wired-build.log`. No repeated broad suite was run.
- Remaining routine work: conversational drafts, trusted enablement
  and standing grants, schedules/product-event triggers, Agents controls and live
  integration/recovery validation. Interrupted/delegated accounting still needs
  release validation. See `docs/routines-beta.md` for deployment limits.

## Durable routine timed waits

- Added fixed wait identities at admission, immutable deadlines, 24-hour expiry
  and an explicit `awaiting_timer` state. Opening the wait pauses active execution
  time and records its status event atomically. Public SDK run records include the
  active wait; host state handling recognizes timer wait/resume phases.
- Connected signed open/resume controls to the existing workflow's durable sleep.
  Go independently resolves timestamps, checks the actual deadline and revalidates
  current provider/target access. Early wakes stay waiting; stale callbacks cannot
  clear later waits. Only Go's completed receipt advances dependent steps.
- Added expiry processing through the existing durable interruption outbox and
  included sleeping runs in pinned-runtime observation. Final completion counts
  waits separately from actual SDK effects. Cancellation retains work already done.
- Wait admission requires `MISTY_ROUTINE_WAITS_ENABLED` and worker negotiation of
  routine-wait protocol 1. Apply `20270129000000_routine_timed_waits.sql` before the
  updated backend. No rollout flag, deployed worker or production migration changed.
- SDK checks passed (86 tests plus packed consumer); public host/app archives and
  server/runtime snapshots were refreshed. Go evidence/deadline-identity checks,
  19 focused runtime tests, runtime build and app typechecking passed. Database
  tests compile but remain unrun.
  Host typechecking reports website-header fixture and file-transfer errors in
  concurrent work; those files were preserved. Evidence:
  `/tmp/misty-routine-timer-sdk.log`, `/tmp/misty-routine-timer-go.log`,
  `/tmp/misty-routine-timer-runtime.log`, `/tmp/misty-routine-timer-build.log`,
  `/tmp/misty-routine-timer-apps-types.log`, `/tmp/misty-routine-timer-host-types.log`.
- Estimated implementation progress is about 70% overall and 85% for the prioritized
  runtime/harness/SDK area. These are rough scope estimates, not measured task counts
  or release-readiness claims. Live integration and the seven-day pilot remain open.

## Next implementation work, in dependency order

1. Validate the implemented quick-AI/Space intervention path against deployed
   workers and host-reported authentication changes; global notifications are connected. Finish authority coverage and atomicity across *all* entry points: app-specific
   history/control access, account handlers, context attachments, native commands,
   completion publication, and every resume. Persist complete admission budgets,
   scope/grant references across all run types; the core run tables now pin their adapter. Installation
   generations now prevent reinstalls from reviving old app admissions. Connect
   remaining native adapters to the implemented deadline/interruption controls;
   validate deployed runtime pause/restart recovery. Deploy the execution-clock
   and device-control migrations and routes before the upgraded coordinator/host,
   retaining existing pinned workers. Extend complete accounting to
   delegated work. Make cancellation UI reflect local effect state.
2. Add explicit assigned/custom-agent policy controls to the working conversational
   and quick AI provider paths. Extend the implemented trusted Activity review to
   inline Agents/run controls and durable approval events. Add account
   target selection through trusted controls, clear unavailable-target presentation,
   and durable sign-in/intervention recovery. Preserve admission snapshots, current
   scope intersections and the shared effect journal. Finish browser binding host validation and native/view bindings.
3. Add target-bound native/browser/backend/view adapters, schema validation,
   immutable version pinning, cancellation/deadlines and provider availability.
   Uninstall and revocation must stop queued work immediately. Pin and retain
   old workers during deployment/rollback; legacy replay hashes cannot reconstruct
   historical logical call IDs for arbitrarily changed inputs.
4. Validate the connected capability/agent/timed-wait coordinator against the
   disposable database and pinned worker, including checkpoint/effect/usage
   boundaries and restart recovery. Keep Vercel details inside its supported adapter.
5. Independently install the packaged habit-tracker backend sample (adapter,
   signing/client helpers and isolated package tests are implemented). Prove
   `habits.list`/`habits.record` plus a Journal summary through conversational
   provider registration/discovery, with no official-app exception or harness
   branching.
6. Connect semantic Inbox/Social browser execution: verified account targets,
   authorized origins, reopen/session recovery, attachment handling, bounded
   planning and protected checkpoints. Reconcile uncertain sends using evidence.
   General browser primitives are not themselves a validated mailbox driver.
7. Complete Files, Code and Terminal actions, stale-write rejection, script-content
   approval hashes, bounded output and cancellation. Complete remaining Journal,
   Planner, Library, Browser and Agents catalog gaps.
8. Build on the implemented immutable drafts and manual backend routine runs:
   add conversational draft authoring, standing grants and trusted enablement;
   durable manual/scheduled/Planner/Journal triggers, DST behavior, coalescing,
   outage catch-up, feedback suppression, budgets and explicit data references.
   Build Agents routine authoring, test/review/enable, history and recovery controls.
9. Add rollout flags, kill switches, monitoring and legacy admission/drain controls.
   Validate all required cross-app scenarios on macOS, real signed-in Gmail and
   Outlook, and the actual Social platforms delivered by the browser work.
10. Run the required seven-day internal pilot with restarts and sleep/reconnect.
    Release only after authorization, duplicate-effect and false-success gates pass.

No real-service compatibility, complete ten-app coverage, packaged independent
sample-app proof, complete saved-routine functionality or production readiness is claimed by this change.
