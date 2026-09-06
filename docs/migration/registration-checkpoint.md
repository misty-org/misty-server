# Current registration checkpoint

The generated [native-coverage.json](native-coverage.json) compares the current
Go golden with actual Hono factory registrations, and distinguishes available
main composition from factories not yet composed. [go-inventory.json](go-inventory.json)
retains static discovery candidates for source review. Run `npm run migration:inventory`
and `npm run migration:coverage` to refresh; the latter builds first. The focused
Go TestRouteInventory passes against the unchanged1232-line route golden.

| Baseline registration state | Verb/path aliases |
| --- | ---: |
| Available in main with required feature configuration |519|
| Factory exists, absent from main |8|
| Native registration absent |705|

Missing aliases represent241 canonical verb/path operations after removing the
supported `/api` and `/v1` prefixes and normalizing parameter names. They include
72 Library/cloud/search,82 Misty/agents/tools,59 integrations/realtime,26 other API,
1 health and1 legacy Stripe URL operation. Family assignment is organizational;
actual route strings remain in the report. Registration does not prove complete
behavior, configuration availability, worker ownership or release readiness.

All74 public HTTP SDK methods have native dispatch owners. All117 Planner and75
Journal baseline aliases have native registrations. Internal workflows, Agent
execution, realtime delivery, retention and ownership handover remain separately
required. Account deletion and runtime callbacks are implemented factories missing
from main while their service prerequisites are completed.

The baseline deliberately excludes some conditional mounts. The report therefore
also captures19 device operations and all aliases, protected metrics, Activepieces
proxy paths, configured Stripe URL routing and hosted/self-host distinctions.
Library routes already appear in the baseline even when their storage dependency
is not usable. Metrics and shared persisted abuse-block refresh remain operational
features to port. Database.Start checks connection/schema and starts no database
worker. Realtime startup is a separate missing service obligation.

All12 startup worker roots and their direct service calls are recorded, along
with9 named native polling workers. Existing native counterparts are not taken as
proof of handover: each legacy job family remains assigned to Go until explicit
ownership transfer and missing operations are complete. Command inventory includes
the API, three self-host admin actions, collaboration-ticket minting and Smart
Library evaluation; the last two still need native command entrypoints. Existing
migration/build/deployment scripts remain part of R10/R12 retirement review.

Library reads, reauthentication, downloads, item mutations and album/folder/group
organization now have81 composed aliases. Sixteen restricted-role PostgreSQL
Library cases pass with typecheck/build; see [library-cutover.md](library-cutover.md).
The next implementation is asset-stack and edit-version metadata operations.
Rendering, provider import, indexing, processing and reconciliation remain open.
No live provider, paid evaluation, deployment or Go shutdown was performed.
