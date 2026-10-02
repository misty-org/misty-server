# CLI, environment, and secrets audit — 2026-09-27

## Result

The audit is complete for this checkout's CLI and configuration surface. It found
additional dead code and configuration defects after the initial cleanup. The
safe local fixes below are implemented. **The entire development, production,
and release surface cannot yet be described as free of stale implementations.**
The remaining findings are listed explicitly; an audit is not a certification
that every external deployment, provider, or platform has been exercised.

Scope: all 23 Rust CLI modules, all TypeScript task entrypoints and their helper/test
files (33 files remain), every top-level command group, all 161 registered server
settings, the three optional CLI configuration files, app `.env` loading, all three
Compose stacks, setup/entrypoint/migration scripts, and the local environment and
Worker-secret files. Both literal and indirect environment reads (provider routing,
feature flags, external tools) were considered. No secret values are in this report.
Remote account configuration, OS Keychain contents, and separate sibling repositories
are outside this checkout audit.

## Remaining findings

| Priority | Finding and evidence | Effect / remaining work |
| --- | --- | --- |
| Resolved | `server/compose.prod.yml` had no Agent runtime. | Resolved 2026-10-01: production now runs the agent runtime and its workflow database on the shared PostgreSQL server, and `PROD_REQUIRED` includes `MISTY_AGENT_RUNTIME_IMAGE`, `MISTY_AGENT_RUNTIME_CONTROL_SECRET` and `AGENT_RUNTIME_DB_PASSWORD`. Self-hosting was retired at the same time. |
| High | Rust `cli/src/release/model.rs` publishes `misty-v…` tags to `misty-org/misty-public`; the active TypeScript beta tasks and `.github/workflows/macos-beta.yml` use `v…` tags in `misty-org/misty`, separate architecture artifacts, `release/trust.json`, and the beta feed. | Two incompatible release systems remain publicly callable. Consolidate around the current release contract or explicitly support both with separate documentation. Merely changing the repository string would leave manifests, signatures, feed publication, and platform handling incompatible. The old route was not deleted based solely on lack of internal callers. |
| Medium | `.config/tooling.json` exposes Wrangler, but root `package.json` does not declare it and the installed resolver fails for `wrangler`. | `misty tool wrangler` and `npm run deploy:web:dev` cannot currently resolve their deployment tool. Add an explicit root dependency or deliberately route this command to a package that owns it. Worker deployment has its own separate Wrangler dependency. |
| Medium | Compose's service `env_file` values are not overridden by arbitrary shell exports. Only values referenced by `${…}` in the Compose file get CLI/shell precedence. | The earlier blanket shell-precedence claim was incorrect. BYOK, runtime authentication, and the development tunnel/deploy tokens are explicitly wired; other settings such as an exported Mailjet credential still require editing their owning env file. Unify this transport before claiming shell-only configuration works for every registered setting. |
| Medium | `cli/src/cli.rs::report_release_inputs` prints missing release inputs without failing `doctor release`, and treats an empty signing-key password as missing. | Release diagnostics can exit successfully with incomplete inputs, or report an optional empty password as missing. Align this with the chosen release flow rather than extending the older release contract in isolation. |
| Medium | Development server startup still invokes `sh`; `server url` invokes `dig`. | Native Windows support for these server commands depends on additional Unix tooling and was not verified. Rust compilation/argument tests are not proof of a working Windows server lifecycle. |
| Low | `MISTY_ROOT` / `MISTY_ORG_ROOT` are accepted in CLI common configuration, but workspace selection happens before that file is loaded (`Settings::load`). | Use shell variables, `--workspace`, or `misty configure` for workspace selection. File-based values cannot select the current command's workspace; this bootstrap inconsistency remains. |
| Low | `server up --detach` is still accepted although startup always detaches, and `server.rs::status` recognizes old one-shot container names. | Compatibility surface, not an active alternate startup implementation. Hide/deprecate the flag when changing the CLI contract; old job labels remain useful when inspecting upgraded stacks. |

BYOK is instance-wide for Agent language models. Voice transcription/TTS and Smart
Library processing retain their own existing provider configuration. They should
not be described as fully converted to BYOK. Mocked provider tests from the prior
implementation passed; no paid direct-provider request was made during this audit.

## Changes made

- Removed the unconsumed server setting `MISTY_LOCAL_WEBSITE_ORIGIN` and the
  write-only `MISTY_CLOUDFLARE_TUNNEL_ID`. Tunnel setup still uses the actual ID in
  Cloudflare requests; it no longer stores an unused copy.
- Retired four CLI-only source settings with no consumers:
  `MISTY_SOURCE_DIR`, `MISTY_PROXY_SOURCE_DIR`, `MISTY_HUB_SOURCE_DIR`, and
  `MISTY_RCLONE_SOURCE`. Old files are tolerated for migration, but these settings
  are never applied. Retired names are rejected by `env set`.
- Removed `MISTY_PROFILE_DIR` and creation of its unused profile directory.
  Native profiles use the active `MISTY_PROFILE` / `MISTY_DESKTOP_PROFILE` identifiers.
- Removed `cli/tasks/staged-app-build.ts` and its tests: this old standalone-app
  build helper had no production callers. Removed unused Rust test dependencies
  `assert_cmd` and `predicates` and pruned their lockfile entries.
- Restricted `misty tasks` / `misty task` to the 12 executable entrypoints. Imported
  helpers and test fixtures no longer appear as runnable commands.
- Unified normal CLI loading with ownership, duplicate-name, permission, and
  unknown-setting checks. Configuration commands can still run without loading
  the legacy file they are intended to migrate.
- Registered the previously omitted active server settings for DB pooling, proxy
  CIDRs, feature switches, provider/model configuration, API endpoint overrides,
  telemetry, and R2 CORS. Added the documented development-signing setting to CLI
  common configuration. `SMART_LIBRARY_EVAL_LIVE` remains a deliberate shell-only
  opt-in for the standalone evaluation command.
- Made CLI validation and port checks respect configured shell overrides for
  Compose interpolation. Startup-log redaction also includes effective overrides.
  Wired Cloudflare tokens/account and deployment mode explicitly into the relevant
  development services.
- Fixed setup/doctor to apply the server environment before using Compose.
  Release tasks now load release configuration; Cloudflare setup/doctor load
  their tooling configuration.
- Made server readiness require a healthy Agent runtime, including detecting a
  missing runtime container.
- Stopped environment migration from overwriting existing scoped files. All
  destination conflicts are detected before writing any migrated server file.
  Invalid CLI migrations no longer leave a temporary directory before parsing.
- Fixed `env set` to replace `export NAME=…` assignments instead of creating
  duplicate keys. Dotenv parse errors now omit the original secret-bearing line.
- Made Worker generation refuse existing bundles instead of silently rotating
  development keys. Setup can restore a missing development Worker half without
  rotation. Validation compares Worker keys with the API's actual file overrides.
- Made production first-use key generation work without manual placeholder files;
  existing room salt is preserved, missing salt is generated, and live keys are
  protected from uncoordinated rotation.
- Removed three identical Journal secret copies from local `crypto/journal.env`
  after comparing them with `.secrets/server.env`. The authoritative generated
  bundle, Worker half, and room salt remain unchanged.
- Fixed the reversed release-start fetch flag: real release starts refresh
  `origin/main`; dry runs do not fetch. This does not reconcile the two release systems.
- Corrected CLI documentation for the in-repository backend, native profile
  storage, optional files, actual Compose precedence, and key-generation behavior.

## Local file inventory and cleanup

Initial snapshot: 28 files and 75 uncommented assignments. After both cleanup
passes: **20 files and 59 assignments**. Removed eight environment files and
16 assignments total; three of those were matching duplicates, not unused keys.
Counts include repeated credentials legitimately used by different processes.
No remaining local environment file is empty or comment-only.

| File / group | Use |
| --- | --- |
| Root `.env` | Two public frontend URLs, loaded by `cli/tasks/app-env.ts` and Vite. Contains no private credentials in this checkout. |
| `server/.env/dev/runtime.env` | API behavior and CLI host port. Some values are intentionally replaced inside Docker; see below. |
| `server/.env/dev/database.env` | API/CLI/migration/test database configuration and Workflow DB password. `DB_SSLMODE` is used by Goose/test scripts; the Go runtime derives SSL mode from host. |
| `server/.env/dev/storage.env` | R2 Library storage credentials and bucket. |
| `server/.env/dev/observability.env` | Metrics endpoint token. |
| `integrations/ai.env` | Existing Gateway credential; optional BYOK comments do not count as configured keys. |
| `integrations/billing.env` | Billing adapter and optional HTTP adapter configuration. |
| `integrations/cloudflare.env` | Tunnel, deployment, account, and callback configuration. |
| `integrations/google.env`, `discord.env`, `microsoft.env` | Active connected-account OAuth configuration. |
| `integrations/instagram.env` | Active Instagram API endpoint override. |
| `integrations/email.env` | Mailjet sender configuration. |
| `crypto/documents.env`, `spaces.env` | Document signing and Space link encryption. |
| `crypto/devices.env` | Device pairing pepper and device ticket signing. |
| `crypto/services.env` | Authentication signing and Agent runtime request authentication. |
| `crypto/journal.env` | Stable Journal room salt after duplicate removal. |
| `server/apps/journal-collab/.secrets/server.env` | API Journal private key plus control/projection secrets; setup copies it to the private runtime volume. |
| `server/apps/journal-collab/.dev.vars` | Matching Worker public key plus control/projection secrets; explicit deployment consumes it. |

All local private server/Worker files have mode 0600. Root `.env` is 0644 and holds
only public URLs. The absent CLI files, optional Dropbox/Figma/GitHub files, and
retired Notion/Slack files are not recreated by init or startup. No production env
or production Worker bundle is present locally; production checks used disposable
fixtures rather than live production keys.

Removed in the first pass: `MISTY_APPS_DIRECTORY`, `DOCUMENT_KEY_ID`,
`DOCUMENT_PRIVATE_KEY_B64`, `MISTY_OPERATOR_USER_ID`, `MISTY_SDK_PROVIDERS_ENABLED`,
`WAITLIST_NOTIFY_EMAIL`, four Notion assignments, and three Slack assignments.
The eight deleted environment files were:
`cli/.env/{common,release,cloudflare}.env` and
`server/.env/dev/integrations/{notion,slack,dropbox,figma,github}.env`.

Container overrides are not proof that a setting has no consumer. In this checkout,
`DB_HOST`, `DB_PORT`, `PORT`, `MISTY_PUBLIC_API_URL`, `MISTY_ALLOWED_ORIGINS`,
`PARTYKIT_HOST`, and the Agent runtime URLs can have different effective Docker
values. Database scripts, CLI port checks, or direct API processes still use their
source values. These were retained and classified, not blindly deleted.

## Command coverage

| Surface | Audit result |
| --- | --- |
| `setup server/desktop/cloudflare/all` | Traced defaults, private-file creation, key preservation, npm setup, Cloudflare preview/apply, and existing-configuration behavior. Live remote provisioning was not run. |
| `tool`, `tasks`, `task` | Traced registry lookup, safe task paths, subprocess arguments, executable dependencies, and task configuration. Wrangler gap remains above. |
| `configure`, `env set/describe/migrate/init/check/status` | Traced workspace selection, owners, precedence, errors, permissions, missing files, required values, and migration writes. Added regression tests for defects found. |
| `home generate/check` | Traced layout, private-file permissions, portable payload allowlist, old-path detection, idempotence, and no-overwrite behavior. Existing tests pass. No user home contents were removed. |
| `doctor all/server/desktop/cloudflare/release` | Traced required tools, configuration, private keys, Compose health, and release reporting. Live server diagnostics pass; release limitations remain above. |
| `check tasks/app/server/website/tools/cli/all` | Verified command dispatch and target package/script existence. CLI suite and task suite were exercised. Full app/native/website/release checks were not substituted for this configuration audit. |
| `desktop dev/build/clean/icons` | Traced profile/route validation, Tauri launch, builds, cleanup guards, and icon generation. Removed unused profile output. No cleanup/build/signing operation was run against user artifacts. |
| `docs dev/build`, `website dev` | Traced calls to the optional sibling website package. That package is outside the configuration cleanup. |
| `server up/status/url/down/logs` | Traced Compose selection, startup sequencing, health, tunnel URL, logs/redaction, and explicit volume deletion flag. Init/check/doctor/up exercised locally. |
| `server prod check/up/down/logs` | Traced required values, Compose, image pull, migrations, and runtime dependency; recorded the production gap rather than testing on live production. |
| `server image build` | Traced canonical Dockerfile/tag handling. No new image was built in this audit. |
| `server worker generate-secrets/deploy`, `server deploy`, `server r2 configure-cors` | Traced key paths, matching bundles, rotation protection, Wrangler inputs, callback URLs, and apply/dry-run flags. Key-generation tests use disposable directories; no deployment or CORS mutation was performed. |
| `release start/build/upload/verify/publish` | Read all release modules, signatures, manifest identities, tags/repositories, artifacts, and explicit publication handling. Recorded the incompatible dual pipelines. No tag, release, upload, signing-key change, or publication occurred. |

## Verification

- Rust: `cargo fmt -- --check`, `cargo clippy --all-targets --locked -- -D warnings`,
  and all **56 tests** pass. Latest CLI installed using `cargo install --path cli --locked`.
- `misty env init dev`: zero new files. `misty env check dev`: valid.
- `misty doctor server --json`: all seven checks ready.
- `misty server up --no-build`: API, Agent runtime, Postgres, and tunnel healthy;
  output is `https://dev-api.mistysys.com/v1`.
- `server/scripts/check-container-contract.sh`: passes.
- `npm run test:tasks`: **47 of 48 passed** at the time of the audit. The Vite entry
  scan failed on `src/features/workspace/useMultiPanelStore.ts:236` (`Unexpected
  ","`), a concurrently edited frontend file outside these changes. It was not
  changed by this audit.
- Tool resolver: Vite, Vitest, ESLint, Prettier, and audit-ci resolve locally;
  Wrangler fails as documented above. Gitleaks resolves to an external executable;
  that resolution alone does not validate its installation or run a secret scan.
- Source inventory: all **161** registered server settings have a source/config
  reference. This is backed by review of owners and overrides; reference matching
  alone cannot prove live use.
- Local inventory after init/startup: 20 files, 59 active assignments, no empty
  environment files, no regenerated placeholders, and private files remain 0600.
- No production credentials, real provider request, external provisioning,
  deployment, release, or publication was exercised. No existing keys were rotated.

## Registered server settings: source evidence

The table maps every registered server name to its owning file and representative
source references. References demonstrate code/config usage, not that an optional
integration is enabled or that every Compose stack forwards it. External SDK/tool
variables and deliberate compatibility aliases are called out separately below.

| Setting | Owner under `server/.env/{dev,prod}/` | Representative consumer |
| --- | --- | --- |
| `AUTH_HANDOFF_START_URL` | `runtime.env` | `server/internal/app/server_environment.go` |
| `MISTY_AGENT_RUNTIME_IMAGE` | `runtime.env` | `server/compose.prod.yml` |
| `MISTY_AGENT_RUNTIME_INTERNAL_API_URL` | `runtime.env` | `server/internal/platform/httpapi/agent_runtime_config.go` · `server/compose.dev.yml` |
| `MISTY_AGENT_RUNTIME_URL` | `runtime.env` | `server/internal/platform/httpapi/agent_runtime_config.go` · `server/compose.dev.yml` |
| `MISTY_ALLOWED_ORIGINS` | `runtime.env` | `server/internal/app/server_mount_drawing_routes.go` · `server/compose.dev.yml` |
| `MISTY_API_IMAGE` | `runtime.env` | `server/compose.dev.yml` · `server/compose.prod.yml` |
| `MISTY_ENVIRONMENT` | `runtime.env` | `server/internal/app/health.go` · `server/internal/platform/config/billing.go` |
| `MISTY_HOST_PORT` | `runtime.env` | `server/compose.dev.yml` · `server/compose.prod.yml` |
| `MISTY_INSTANCE_NAME` | `runtime.env` | `server/internal/platform/httpapi/instance.go` · `server/internal/app/production_environment.go` |
| `MISTY_PUBLIC_API_URL` | `runtime.env` | `.config/vite.config.ts` · `server/internal/app/health.go` |
| `MISTY_SDK_EXECUTION_ENABLED` | `runtime.env` | `server/internal/platform/httpapi/sdk_invocation_runtime.go` · `server/compose.dev.yml` |
| `MISTY_DEVICE_JOBS_ENABLED` | `runtime.env` | `server/internal/app/server_mount_spaces_routes.go` · `server/compose.dev.yml` |
| `MISTY_WEBSITE_URL` | `runtime.env` | `server/internal/app/server_environment.go` |
| `PASSWORD_RESET_START_URL` | `runtime.env` | `server/internal/app/server_environment.go` |
| `PASSWORD_RESET_URL` | `runtime.env` | `server/internal/app/server_environment.go` |
| `PORT` | `runtime.env` | `server/internal/app/production_environment.go` |
| `TRUST_PROXY_HEADERS` | `runtime.env` | `server/internal/app/server_environment.go` · `server/internal/platform/httpapi/client_ip.go` |
| `TRUSTED_PROXY_CIDRS` | `runtime.env` | `server/internal/app/server_environment.go` · `server/internal/platform/httpapi/client_ip.go` |
| `MISTY_INVITATION_URL_BASE` | `runtime.env` | `server/internal/app/server_core.go` |
| `MISTY_AGENT_DOCUMENTS_ENABLED` | `runtime.env` | `server/internal/platform/httpapi/ai_complete.go` |
| `MISTY_SOCIAL_AUTOMATION_DISABLED` | `runtime.env` | `server/internal/platform/httpapi/social_worker.go` |
| `MISTY_SOCIAL_DISCORD_DISABLED` | `runtime.env` | `server/internal/platform/httpapi/social_discord_gateway.go` |
| `MISTY_SOCIAL_SEND_DISABLED` | `runtime.env` | `server/internal/platform/httpapi/social_worker.go` |
| `AGENT_RUNTIME_DB_PASSWORD` | `database.env` | `server/scripts/test-consolidated-setup.py` · `server/compose.dev.yml` |
| `DB_HOST` | `database.env` | `server/internal/platform/postgres/db.go` · `server/internal/app/production_environment.go` |
| `DB_MIGRATION_PASSWORD` | `database.env` | `server/compose.dev.yml` · `server/compose.prod.yml` |
| `DB_MIGRATION_USER` | `database.env` | `server/compose.dev.yml` · `server/compose.prod.yml` |
| `DB_NAME` | `database.env` | `server/internal/platform/postgres/db.go` · `server/internal/app/production_environment.go` |
| `DB_PASSWORD` | `database.env` | `server/internal/platform/postgres/db.go` · `server/internal/app/production_environment.go` |
| `DB_PORT` | `database.env` | `server/internal/platform/postgres/db.go` · `server/compose.dev.yml` |
| `DB_SSLMODE` | `database.env` | `server/scripts/goose.sh` · `server/scripts/test-browser-server.sh` |
| `DB_MAX_OPEN_CONNS` | `database.env` | `server/internal/platform/postgres/db.go` |
| `DB_MAX_IDLE_CONNS` | `database.env` | `server/internal/platform/postgres/db.go` |
| `DB_CONN_MAX_LIFETIME` | `database.env` | `server/internal/platform/postgres/db.go` |
| `DB_CONN_MAX_IDLE_TIME` | `database.env` | `server/internal/platform/postgres/db.go` |
| `DB_USER` | `database.env` | `server/internal/platform/postgres/db.go` · `server/internal/app/production_environment.go` |
| `MISTY_LIBRARY_BACKEND` | `storage.env` | `server/internal/platform/httpapi/instance.go` · `server/internal/app/production_environment.go` |
| `MISTY_LIBRARY_FILESYSTEM_DIR` | `storage.env` | `server/internal/platform/httpapi/instance.go` · `server/internal/app/production_environment.go` |
| `MISTY_S3_ACCESS_KEY_ID` | `storage.env` | `server/internal/app/production_environment.go` · `server/internal/app/server_mount_drawing_routes.go` |
| `MISTY_S3_BUCKET` | `storage.env` | `server/internal/app/production_environment.go` · `server/internal/app/server_mount_drawing_routes.go` |
| `MISTY_S3_ENDPOINT` | `storage.env` | `server/internal/app/production_environment.go` · `server/internal/app/server_mount_drawing_routes.go` |
| `MISTY_S3_FORCE_PATH_STYLE` | `storage.env` | `server/internal/app/server_mount_drawing_routes.go` |
| `MISTY_S3_REGION` | `storage.env` | `server/internal/app/production_environment.go` · `server/internal/app/server_mount_drawing_routes.go` |
| `MISTY_S3_SECRET_ACCESS_KEY` | `storage.env` | `server/internal/app/production_environment.go` · `server/internal/app/server_mount_drawing_routes.go` |
| `R2_ACCESS_KEY` | `storage.env` | `server/internal/app/production_environment.go` · `server/internal/app/server_mount_drawing_routes.go` |
| `R2_BUCKET` | `storage.env` | `server/internal/app/production_environment.go` · `server/internal/app/server_mount_drawing_routes.go` |
| `R2_ENDPOINT` | `storage.env` | `server/internal/app/production_environment.go` · `server/internal/app/server_mount_drawing_routes.go` |
| `R2_SECRET_KEY` | `storage.env` | `server/internal/app/production_environment.go` · `server/internal/app/server_mount_drawing_routes.go` |
| `MISTY_R2_ALLOWED_ORIGINS` | `storage.env` | `cli/src/server.rs` |
| `MISTY_METRICS_TOKEN` | `observability.env` | `server/internal/app/server_metrics.go` |
| `POSTHOG_PROJECT_TOKEN` | `observability.env` | `src-tauri/build.rs` · `.config/vite.config.ts` |
| `POSTHOG_HOST` | `observability.env` | `src-tauri/build.rs` · `.config/vite.config.ts` |
| `MISTY_RELEASE_CHANNEL` | `observability.env` | `src-tauri/build.rs` · `src-tauri/src/telemetry.rs` |
| `MISTY_SERVER_VERSION` | `observability.env` | `server/internal/app/health.go` · `server/internal/platform/telemetry/client.go` |
| `AI_GATEWAY_API_KEY` | `integrations/ai.env` | `server/internal/app/health.go` · `server/internal/app/server_core.go` |
| `AI_GATEWAY_BASE_URL` | `integrations/ai.env` | `server/internal/app/server_core.go` · `server/internal/agents/provider_config.go` |
| `AI_GATEWAY_EMBEDDING_BASE_URL` | `integrations/ai.env` | `server/internal/agents/smart_library_analyzer_request_at.go` |
| `AGENT_TRANSCRIPTION_MODEL` | `integrations/ai.env` | `server/internal/agents/media_search.go` |
| `MEDIA_SEARCH_EMERGENCY_DISABLE` | `integrations/ai.env` | `server/internal/platform/httpapi/media_search_index_chunk.go` |
| `MEDIA_SEARCH_TRANSCRIPTION_MODEL` | `integrations/ai.env` | `server/internal/agents/media_search.go` |
| `MEDIA_SEARCH_TRANSCRIPTION_FALLBACK_MODEL` | `integrations/ai.env` | `server/internal/agents/media_search.go` |
| `SMART_LIBRARY_EMBEDDING_MODEL` | `integrations/ai.env` | `server/internal/agents/smart_library_analyzer_request_at.go` · `server/internal/platform/httpapi/smart_library_hosted_ai_weekly_ratio.go` |
| `SMART_LIBRARY_PRIMARY_MODEL` | `integrations/ai.env` | `server/internal/agents/smart_library_analyzer_request_at.go` |
| `SMART_LIBRARY_FALLBACK_MODEL` | `integrations/ai.env` | `server/internal/agents/smart_library_analyzer_request_at.go` |
| `SMART_LIBRARY_EMERGENCY_DISABLE` | `integrations/ai.env` | `server/internal/platform/httpapi/smart_library_approve.go` · `server/internal/platform/httpapi/smart_library_complete_reindex.go` |
| `SMART_LIBRARY_SEARCH_EMERGENCY_DISABLE` | `integrations/ai.env` | `server/internal/platform/httpapi/smart_library_set_asset_tags.go` |
| `SMART_LIBRARY_SEARCH_DAILY_LIMIT` | `integrations/ai.env` | `server/internal/platform/httpapi/smart_library_hosted_ai_weekly_ratio.go` |
| `MISTY_AI_PROVIDER` | `integrations/ai.env` | `server/internal/agents/provider_config.go` |
| `MISTY_AI_MODEL` | `integrations/ai.env` | `server/internal/agents/provider_config.go` |
| `MISTY_AI_LOW_MODEL` | `integrations/ai.env` | `server/internal/agents/provider_config.go` · `server/internal/agents/model_catalog_version.go` |
| `MISTY_AI_MED_MODEL` | `integrations/ai.env` | `server/internal/agents/provider_config.go` · `server/internal/agents/model_catalog_version.go` |
| `MISTY_AI_HIGH_MODEL` | `integrations/ai.env` | `server/internal/agents/provider_config.go` · `server/internal/agents/model_catalog_version.go` |
| `MISTY_AI_MODEL_CATALOG_JSON` | `integrations/ai.env` | `server/internal/agents/model_catalog_version.go` |
| `OPENAI_BASE_URL` | `integrations/ai.env` | `server/internal/agents/provider_config.go` |
| `GEMINI_AUTH_MODE` | `integrations/ai.env` | `server/internal/agents/gemini_provider.go` · `server/internal/agents/provider_config.go` |
| `GEMINI_BASE_URL` | `integrations/ai.env` | `server/internal/agents/provider_config.go` |
| `GEMINI_OAUTH_SCOPE` | `integrations/ai.env` | `server/internal/agents/provider_config.go` |
| `GEMINI_VERTEX_PROJECT` | `integrations/ai.env` | `server/internal/agents/provider_config.go` |
| `GEMINI_VERTEX_LOCATION` | `integrations/ai.env` | `server/internal/agents/provider_config.go` |
| `GOOGLE_CLOUD_PROJECT` | `integrations/ai.env` | `server/internal/agents/provider_config.go` |
| `GOOGLE_CLOUD_LOCATION` | `integrations/ai.env` | `server/internal/agents/provider_config.go` |
| `GOOGLE_CLOUD_REGION` | `integrations/ai.env` | `server/internal/agents/provider_config.go` |
| `VISION_PROCESSOR_URL` | `integrations/ai.env` | `server/internal/app/server_core.go` |
| `VISION_PROCESSOR_TOKEN` | `integrations/ai.env` | `server/internal/app/server_core.go` |
| `MISTY_AGENT_MODEL_PROVIDER` | `integrations/ai.env` | `server/internal/platform/config/agent_model.go` · `server/apps/agent-runtime/src/model-provider.ts` |
| `MISTY_AGENT_MODEL` | `integrations/ai.env` | `server/internal/platform/config/agent_model.go` · `server/apps/agent-runtime/src/model-provider.ts` |
| `MISTY_AGENT_MODEL_API_KEY` | `integrations/ai.env` | `server/internal/platform/config/agent_model.go` · `server/apps/agent-runtime/src/model-provider.ts` |
| `MISTY_AGENT_MODEL_BASE_URL` | `integrations/ai.env` | `server/internal/platform/config/agent_model.go` · `server/apps/agent-runtime/src/model-provider.ts` |
| `GEMINI_API_KEY` | `integrations/ai.env` | `server/internal/agents/gemini_provider.go` · `server/internal/agents/provider_config.go` |
| `GOOGLE_API_KEY` | `integrations/ai.env` | `server/internal/agents/provider_config.go` |
| `MISTY_AI_MAX_TOKENS_PER_DAY` | `integrations/ai.env` | `server/internal/agents/provider_budget_limits.go` |
| `MISTY_AI_MAX_TOKENS_PER_HOUR` | `integrations/ai.env` | `server/internal/agents/provider_budget_limits.go` |
| `OPENAI_API_KEY` | `integrations/ai.env` | `src-tauri/src/infra/smart_library.rs` · `server/internal/agents/provider_config.go` |
| `VERCEL_OIDC_TOKEN` | `integrations/ai.env` | `server/internal/app/health.go` · `server/internal/agents/provider_config.go` |
| `CLOUDFLARE_ACCOUNT_ID` | `integrations/cloudflare.env` | `server/compose.dev.yml` · `cli/src/cloudflare.rs` |
| `CLOUDFLARE_ZONE_ID` | `integrations/cloudflare.env` | `cli/src/cloudflare.rs` |
| `MISTY_CLOUDFLARE_TUNNEL_NAME` | `integrations/cloudflare.env` | `cli/src/cloudflare.rs` |
| `CLOUDFLARE_API_TOKEN` | `integrations/cloudflare.env` | `server/scripts/test-consolidated-setup.py` · `server/compose.dev.yml` |
| `CLOUDFLARE_TUNNEL_TOKEN` | `integrations/cloudflare.env` | `server/compose.dev.yml` · `server/scripts/check-container-contract.sh` |
| `MISTY_CLOUDFLARE_WORKER_HOST` | `integrations/cloudflare.env` | `server/compose.dev.yml` · `cli/src/cloudflare.rs` |
| `MISTY_CLOUDFLARE_WORKER_NAME` | `integrations/cloudflare.env` | `server/compose.dev.yml` · `server/apps/journal-collab/docker/cloudflare-deploy.sh` |
| `MISTY_DEV_ALLOWED_ORIGINS` | `integrations/cloudflare.env` | `server/compose.dev.yml` |
| `MISTY_DEV_API_ORIGIN` | `integrations/cloudflare.env` | `server/compose.dev.yml` · `server/scripts/check-container-contract.sh` |
| `MISTY_DEV_API_TUNNEL_HOSTNAME` | `integrations/cloudflare.env` | `cli/src/server.rs` · `cli/src/cloudflare.rs` |
| `MISTY_DEV_TUNNEL_HOSTNAME` | `integrations/cloudflare.env` | `cli/src/server.rs` |
| `PARTYKIT_HOST` | `integrations/cloudflare.env` | `server/internal/app/production_environment.go` · `server/internal/platform/httpapi/journal_collab_config.go` |
| `MISTY_BILLING_ADAPTER` | `integrations/billing.env` | `server/internal/app/health.go` · `server/internal/platform/config/billing.go` |
| `MISTY_BILLING_URL` | `integrations/billing.env` | `server/internal/app/health.go` · `server/internal/platform/config/billing.go` |
| `MISTY_BILLING_SECRET` | `integrations/billing.env` | `server/internal/app/health.go` · `server/internal/platform/config/billing.go` |
| `DISCORD_BOT_TOKEN` | `integrations/discord.env` | `server/internal/platform/httpapi/social.go` · `server/internal/platform/httpapi/social_worker.go` |
| `DISCORD_GATEWAY_URL` | `integrations/discord.env` | `server/internal/platform/httpapi/social_discord_gateway.go` |
| `DISCORD_CLIENT_ID` | `integrations/discord.env` | `server/internal/platform/httpapi/connected_accounts_oauth.go` |
| `DISCORD_CLIENT_SECRET` | `integrations/discord.env` | `server/internal/platform/httpapi/connected_accounts_oauth.go` |
| `MISTY_DROPBOX_CLIENT_ID` | `integrations/dropbox.env` | `server/internal/platform/httpapi/connected_accounts_oauth.go` · `server/internal/platform/httpapi/cloud_connections_cloud_o_auth_definition.go` |
| `MISTY_DROPBOX_CLIENT_SECRET` | `integrations/dropbox.env` | `server/internal/platform/httpapi/connected_accounts_oauth.go` · `server/internal/platform/httpapi/cloud_connections_cloud_o_auth_definition.go` |
| `MAILJET_API_KEY` | `integrations/email.env` | `server/internal/app/health.go` · `server/internal/platform/email/sender_config.go` |
| `MAILJET_API_BASE_URL` | `integrations/email.env` | `server/internal/platform/email/sender_config.go` |
| `MAILJET_FROM_EMAIL` | `integrations/email.env` | `server/internal/app/health.go` · `server/internal/platform/email/sender_config.go` |
| `MAILJET_FROM_NAME` | `integrations/email.env` | `server/internal/platform/email/sender_config.go` |
| `MAILJET_SECRET_KEY` | `integrations/email.env` | `server/internal/app/health.go` · `server/internal/platform/email/sender_config.go` |
| `FIGMA_CLIENT_ID` | `integrations/figma.env` | `server/internal/platform/httpapi/connected_accounts_oauth.go` |
| `FIGMA_CLIENT_SECRET` | `integrations/figma.env` | `server/internal/platform/httpapi/connected_accounts_oauth.go` |
| `FIGMA_API_BASE_URL` | `integrations/figma.env` | `server/internal/platform/httpapi/figma_client.go` |
| `GITHUB_APP_ID` | `integrations/github.env` | `server/internal/platform/httpapi/github_app_client.go` · `server/internal/platform/httpapi/provider_integrations_provider_o_auth_definition.go` |
| `GITHUB_API_BASE_URL` | `integrations/github.env` | `server/internal/platform/httpapi/github_app_client.go` |
| `GITHUB_APP_PRIVATE_KEY` | `integrations/github.env` | `server/internal/platform/httpapi/github_app_client.go` · `server/internal/platform/httpapi/provider_integrations_provider_o_auth_definition.go` |
| `GITHUB_APP_SLUG` | `integrations/github.env` | `server/internal/platform/httpapi/github_app_http.go` · `server/internal/platform/httpapi/provider_integrations_provider_o_auth_definition.go` |
| `GITHUB_WEBHOOK_SECRET` | `integrations/github.env` | `server/internal/platform/httpapi/github_webhook.go` · `server/internal/platform/httpapi/provider_integrations_provider_o_auth_definition.go` |
| `GOOGLE_CLIENT_ID` | `integrations/google.env` | `server/internal/platform/httpapi/connected_accounts_oauth.go` |
| `GOOGLE_CLIENT_SECRET` | `integrations/google.env` | `server/internal/platform/httpapi/connected_accounts_oauth.go` |
| `MISTY_GOOGLE_DRIVE_CLIENT_ID` | `integrations/google.env` | `server/internal/platform/httpapi/cloud_connections_cloud_o_auth_definition.go` |
| `MISTY_GOOGLE_DRIVE_CLIENT_SECRET` | `integrations/google.env` | `server/internal/platform/httpapi/cloud_connections_cloud_o_auth_definition.go` |
| `INSTAGRAM_APP_SECRET` | `integrations/instagram.env` | `server/internal/platform/httpapi/social_webhooks.go` |
| `INSTAGRAM_CLIENT_ID` | `integrations/instagram.env` | `server/internal/platform/httpapi/connected_accounts_oauth.go` |
| `INSTAGRAM_CLIENT_SECRET` | `integrations/instagram.env` | `server/internal/platform/httpapi/connected_accounts_oauth.go` |
| `INSTAGRAM_GRAPH_API_BASE_URL` | `integrations/instagram.env` | `server/internal/platform/httpapi/social.go` · `server/internal/platform/httpapi/social_worker.go` |
| `INSTAGRAM_WEBHOOK_VERIFY_TOKEN` | `integrations/instagram.env` | `server/internal/platform/httpapi/social_webhooks.go` |
| `MICROSOFT_CLIENT_ID` | `integrations/microsoft.env` | `server/internal/platform/httpapi/connected_accounts_oauth.go` |
| `MICROSOFT_CLIENT_SECRET` | `integrations/microsoft.env` | `server/internal/platform/httpapi/connected_accounts_oauth.go` |
| `MISTY_ONEDRIVE_CLIENT_ID` | `integrations/microsoft.env` | `server/internal/platform/httpapi/cloud_connections_cloud_o_auth_definition.go` |
| `MISTY_ONEDRIVE_CLIENT_SECRET` | `integrations/microsoft.env` | `server/internal/platform/httpapi/cloud_connections_cloud_o_auth_definition.go` |
| `DOCUMENT_SIGNING_KEY` | `crypto/documents.env` | `server/internal/app/server_mount_spaces_routes.go` |
| `JOURNAL_COLLAB_CONTROL_SECRET` | `crypto/journal.env` | `server/internal/platform/httpapi/journal_collab_config.go` |
| `JOURNAL_COLLAB_CONTROL_SECRET_PREVIOUS` | `crypto/journal.env` | `server/apps/journal-collab/src/document-room.ts` |
| `JOURNAL_COLLAB_PROJECTION_SECRET` | `crypto/journal.env` | `server/internal/platform/httpapi/journal_collab_config.go` |
| `JOURNAL_COLLAB_PROJECTION_SECRET_PREVIOUS` | `crypto/journal.env` | `server/apps/journal-collab/src/document-room.ts` · `server/internal/platform/httpapi/journal_collab_config.go` |
| `JOURNAL_COLLAB_ROOM_SALT` | `crypto/journal.env` | `server/internal/platform/httpapi/journal_collab_config.go` |
| `JOURNAL_COLLAB_TICKET_PRIVATE_KEY` | `crypto/journal.env` | `server/internal/app/production_environment.go` · `server/internal/platform/httpapi/journal_collab_config.go` |
| `JOURNAL_COLLAB_TICKET_PUBLIC_KEY` | `crypto/journal.env` | `server/apps/journal-collab/src/document-room.ts` |
| `MISTY_DEVICE_PAIRING_PEPPER` | `crypto/devices.env` | `server/internal/app/server_mount_drawing_routes.go` · `server/internal/platform/httpapi/connected_devices_config.go` |
| `MISTY_DEVICE_TICKET_PREVIOUS_PUBLIC_KEYS` | `crypto/devices.env` | `server/internal/platform/httpapi/connected_devices_config.go` |
| `MISTY_DEVICE_TICKET_PRIVATE_KEY` | `crypto/devices.env` | `server/internal/app/server_mount_drawing_routes.go` · `server/internal/platform/httpapi/connected_devices_config.go` |
| `SPACE_LINK_ENCRYPTION_KEY` | `crypto/spaces.env` | `server/internal/app/production_environment.go` · `server/internal/app/server_mount_spaces_routes.go` |
| `MISTY_AGENT_RUNTIME_CONTROL_SECRET` | `crypto/services.env` | `server/apps/agent-runtime/src/signature.ts` · `server/internal/platform/httpapi/agent_runtime_config.go` |
| `MISTY_AGENT_RUNTIME_CONTROL_SECRET_PREVIOUS` | `crypto/services.env` | `server/apps/agent-runtime/src/index.ts` · `server/internal/platform/httpapi/agent_runtime_config.go` |
| `MISTY_AUTH_SIGNING_KEY` | `crypto/services.env` | `server/internal/platform/security/session_jwt.go` |
| `MISTY_AUTH_SIGNING_KEY_PREVIOUS` | `crypto/services.env` | `server/internal/platform/security/session_jwt.go` |

## CLI and external configuration contracts retained

`cli/.env/common.env` accepts workspace aliases (with the bootstrap caveat above),
`MISTY_DESKTOP_DEV_PORT`, `MISTY_DESKTOP_INITIAL_ROUTE`, and
`MISTY_DEV_SIGNING_IDENTITY`. It is absent locally until configured.

`cli/.env/release.env` permits the `TAURI_*`, `APPLE_*`, `WINDOWS_*`,
`MISTY_CODESIGN_*`, and `MISTY_NOTARY_*` families because release tooling and Tauri
consume them. Explicit local reads include updater public key/endpoint, CSP origins,
Windows certificate thumbprint/timestamp URL, Apple signing identity, and notary
keychain profile. Tauri consumes signing-private-key/password and Apple notarization
credentials. This extensible family policy is not a claim that every imaginable
name with those prefixes is meaningful. `MISTY_CODESIGN_IDENTITY` remains an active
compatibility alias for `APPLE_SIGNING_IDENTITY`.

`cli/.env/cloudflare.env` permits `CLOUDFLARE_*`, `R2_*`, `MISTY_CLOUDFLARE_*`, and
`MISTY_R2_*` for Wrangler/R2 tooling. All three local CLI files are absent rather than
empty placeholders. Worker runtime bindings such as previous control/projection
secrets must be supplied to the Worker deployment too; registering them in the API
configuration does not automatically deploy them to Cloudflare.

Compatibility readers for old CLI settings paths, scoped-env migration, the old
tunnel hostname alias, and PostgreSQL workflow consolidation remain intentional
upgrade paths. Removing these just because their names are old could strand
existing installs or databases. No old running product/service implementation is
reactivated by the deprecated-name lists.
