# SDK provider installation and registration

This boundary is implemented in Go. Backend target binding is
implemented; runtime capability execution is still under development. Registration
alone does not make a provider executable.
New installations, runtime-session issuance, and registration default to disabled.
Set `MISTY_SDK_PROVIDERS_ENABLED=true` only in an explicitly enabled beta environment.
Disabling it preserves discovery, unregister, uninstall, manifests, and version
records. The forthcoming execution gate must also stop new effect dispatch.

## Reviewed installation

A trusted Misty account session sends `POST /v1/me/sdk-apps/install`:

```json
{
  "manifest": {
    "document": "<exact UTF-8 JSON document>",
    "publicKey": "<base64 Ed25519 public key, 32 bytes>",
    "signature": "<base64 Ed25519 signature, 64 bytes>"
  },
  "reviewedDigest": "<lowercase SHA-256 hex of the document bytes>"
}
```

The signature covers the UTF-8 bytes of `misty.sdk.manifest.v1\n` followed by the
exact document string. The digest covers the document alone. Do not reformat the
document between signing, review, and installation. Duplicate keys, unknown
contract fields, remote schema references, invalid schemas, and excessive sizes
are rejected. Schemas are limited to 64 KiB and the document to 2 MiB.

The document contains:

```json
{
  "appId": "example.habits",
  "version": "1.0.0",
  "permissionVersion": 1,
  "scopes": ["capabilities.providers.write", "capabilities.read", "habits.list", "habits.record"],
  "capabilities": {
    "protocol": 1,
    "providers": ["<MistyCapabilityProvider objects from the SDK contracts>"]
  }
}
```

The first user-reviewed installation pins the signing key for that user and app.
This is continuity verification, not a claim that Misty has vetted the publisher
or verified ownership of a domain. Another key cannot replace the installed app.
Catalog app identities cannot be installed through this endpoint. App bearers
cannot install apps, mint sessions, or grant their own permissions.

Changing permissions requires a new permission version and explicit user review.
App versions, provider versions, and semantic capability versions are immutable.
Multiple providers can share a semantic version only when their complete contracts
agree. Failed installation checks roll back the entire transaction, including any
proposed permission change. Reserved Inbox/Social contracts are generated from the public SDK snapshot;
providers cannot weaken them. The existing registry has a provider/target
constructor, but live runtime discovery and dispatch still need wiring.

The trusted host obtains a five-minute app credential using
`POST /v1/me/sdk-apps/{appID}/sessions` with `{}` for an account session or
`{"spaceId":"..."}` for a Space session. Space membership is verified. The host
must deliver this restricted credential to the owning app only. Account-scoped
SDK RPC now works without a Space; Space-scoped methods still require a current
membership and a matching bound Space.

## Provider lifecycle

An account-scoped app credential with `capabilities.providers.write` calls the
SDK's `capabilities.providers.register` method. `manifestDigest` is the digest of
the installed signed document above. The provider must exactly match a declaration
in the current installed manifest and its owner prefix must equal the app ID.
Registration cannot add scopes or change the execution route. Downloaded apps
cannot register server or native host adapters. Browser routes additionally need
reviewed browser scopes and exact declared origins.

The backend implements these public SDK methods:

- `capabilities.providers.register`
- `capabilities.providers.unregister`
- `capabilities.providers.availability`
- `capabilities.discover`
- `capabilities.targets.resolve`

Provider IDs in direct HTTP paths must be URL encoded, including the slash. The
SDK RPC resolver performs this encoding. `capabilities.read` permits discovery;
returned capabilities also require both the caller's admission-time scopes and
its current installed scopes. Discovery uses a continuation cursor and does not
truncate the catalog at a fixed number of entries.

Availability reports are observations from the owning app, not authorization or
proof that a target can execute. Older observations cannot replace newer ones;
reports cannot revive an unregistered provider. Executable availability must also
check the bound adapter, device/view lifetime, credentials, target identity, and
current permissions when target execution is connected.

Uninstall, scope changes, and app-version changes retire stale app sessions.
Uninstall/reinstall and version rollback cannot automatically revive old provider
registrations. Provider and contract version records remain for pinning and
recovery. The public uninstall control is `DELETE /v1/me/sdk-apps/{appID}`.

## Backend target controls

Trusted account controls configure a declared backend connection with
`PUT /v1/me/sdk-apps/{appID}/connections/{connectionID}`:

```json
{
  "expectedRevision": 0,
  "endpointURL": "https://habits.example.com/execute",
  "bearerToken": "<provider credential>"
}
```

Zero creates revision 1. Updates require the current revision and always create a
new immutable configuration. The connection ID must occur in this app's current
signed manifest. Credentials are encrypted with associated data binding the user,
app, connection, and revision. Responses contain only the connection ID and revision.
`DELETE` on the same path revokes the connection immediately. The outbound transport
used by the execution handler refuses redirects, environment proxies,
and private/reserved DNS/IP destinations.

Create or refresh a target with `POST /v1/me/sdk-targets`:

```json
{
  "targetId": "<host-chosen stable UUID>",
  "expectedRevision": 0,
  "providerId": "example.habits/backend",
  "providerVersion": 1,
  "spaceId": "<optional existing Misty Space ID; omit for an account target>",
  "label": "My habit account",
  "capabilities": ["habits.list", "habits.record"],
  "callerApps": ["example.habits"]
}
```

This grants target access only, not standing approval for mutations. Caller apps
still need their own current and admission-time capability scopes. An empty caller
list limits access to trusted Misty user controls. Each target pins its connection
configuration revision privately; changing credentials or endpoints invalidates
resolution of the old target until the user refreshes it. Stale edits conflict.
`DELETE /v1/me/sdk-targets/{targetID}` revokes access without deleting history.

`capabilities.targets.resolve` returns only authorized backend targets and preserves
an explicitly requested target. Current Space membership, installation/permission
state, provider version, connection revision and caller access are rechecked.
More than 100 matching targets requires an explicit target selection instead of
silent truncation. Context-bound browser/native/view resolution remains unfinished
and returns an explicit unavailable outcome; no active-tab fallback is used.

### Browser target bindings and trusted inventory

The same trusted target endpoint accepts a browser provider from an installed,
verified manifest. Include `browser` with `kind: "browser"`, `deviceId` from the
user's trusted-device list, the host's 64-character profile digest, a stable
`accountBindingId` UUID, exact HTTPS `origins`, and optionally the SDK browser's
`contextId`. Origins must be a subset of the pinned provider's declared origins.
The control plane rejects foreign or revoked devices and stale revisions. It
stores browser bindings without a backend connection or credential. Configuration
records an **intended** account; it does not verify the website's current account.

`GET /v1/me/sdk-targets?limit=50&cursor=<last-target-UUID>` returns the current
configuration of each target, including `enabled`, `capabilities`, and `callerApps`.
Follow `nextCursor` until null. Disabled and unavailable targets remain visible for
repair and revocation. Listing/configuration requires a trusted user session;
third-party app bearers cannot read or change these grants. The SDK exports
`MistyCapabilityTargetConfigurationSchema`, `MistyBrowserCapabilityBindingSchema`
and `MistyCapabilityTargetPageSchema` for host integrations, without registering an
app RPC method for trusted controls.

Browser targets remain unavailable to invocation and model discovery until the
browser execution adapter validates the actual target and is enabled. Provider
availability reports cannot route them through the backend adapter, and admission
does not pin a nonexistent browser adapter. Native/view target configuration,
verified account selection, context resolution and browser execution remain work
in progress. Existing backend target execution is unchanged.

## Backend invocation

Enable `MISTY_SDK_EXECUTION_ENABLED=true` only with a configured durable runtime.
The independent app needs `capabilities.invoke`, `capabilities.read`, the action's
required scopes and explicit target access. Submit `POST /v1/capabilities/invocations`
with the SDK Invocation contract (request UUID, capability/provider versions,
target UUID/revision, validated input and deadline). The response is 202 with a
request UUID and the public UUID projection of the existing run identity.
Identical request retries share the same run/effect. Changed requests conflict.

`GET /v1/capabilities/invocations/{requestID}` returns state and a typed protected
outcome to the original calling app while its generation and target access remain
valid. `POST /v1/capabilities/invocations/{requestID}/cancel` requests cancellation.
A bound runtime is not shown as cancelled before its effects can be classified.

The adapter receives an authenticated POST to its pinned endpoint with
`{protocol: 1, execution, target}`. Execution includes the original request,
`runId`, stable `effectId`, and `grantIds`. The same effect ID is supplied as
`Idempotency-Key`. Credentials never appear in discovery, model context or results.
Respond with the public success/failure/uncertain shape; successful results must
match the declared output schema and include evidence and the partial flag.
Providers cannot mint host approval or wait identities.

Write declarations cannot omit approval. A scoped declaration also pauses until
Misty has actual user consent; registration does not supply it. Only trusted user
controls can call `POST /v1/me/sdk-runs/{runID}/approvals/{approvalID}` with
`{approved: true|false}`. Before deciding, retrieve
`GET /v1/me/capability-approvals/{approvalID}` from trusted user controls. It returns
`{approval, review}`; review contains the exact execution/input, public target,
declared effects and description. Provider descriptions and input are data, never
instructions for the reviewer. The encrypted snapshot commits with the approval
wait, survives restart, and cannot change on retry. This endpoint accepts both
SDK/AI UUID approvals and conversational `approval_...` identities. It excludes
backend endpoint credentials and runtime continuation secrets, denies app and
runtime tokens, and sends `Cache-Control: no-store`.

The SDK decision endpoint uses the public UUID of the AI invocation, including a
quick AI request. Space conversations keep their existing creator-approval decision
route. A decision publishes an exact durable resume; app bearers are denied. Outcomes are based on journal evidence, not the runtime's final text.
Lost mutation responses remain uncertain, preventing blind re-execution.

Managed Misty conversational runs now discover providers from the Space-bound
snapshot taken at run admission. Account-only targets do not implicitly enter a
Space conversation. Delegation cannot expand the snapshot; changed or revoked
targets cannot replace it. SDK writes still require individual approval, and an
uncertain effect blocks replacement SDK proposals and dependent conversational
writes. Report provider availability explicitly after registration; only an
available provider can resolve for execution. Unavailable authorized targets remain
pinned in a conversational run, so readiness can recover without changing accounts
or implementations.

Quick AI requests pin authorized account or Space targets at admission and use
the same registry, approval reviews, backend dispatcher and effect journal.
`ai.write` alone cannot grant provider access. Later consent does not expand an
old request; linked agent runs inherit both its exact bindings and app authority.
Repeated waits retain distinct identities, and an uncertain action prevents a
replacement proposal with a new effect ID.

Misty's Activity page now discovers and reviews SDK approvals through trusted host
controls. `GET /v1/me/capability-approvals?limit=20&cursor=...` returns owner-scoped
`{approvals, nextCursor?}` metadata for current pending waits; each next cursor is
an opaque approval identity. Limits range from 1 to 100. It omits protected inputs
and excludes expired, cancelled, stale or already decided waits. Read controls
remain available with execution admissions disabled so users retain access to
review/recovery data.

Assigned/custom-agent policy controls, inline Agents approval presentation,
backend authentication waits, browser/native/view execution and the actual
packaged sample-app/model proof remain unfinished. No live website compatibility
is claimed.

## Validation

`./scripts/test-automation-beta.sh` provisions and removes its own disposable
PostgreSQL database. Provider tests cover signature tampering, duplicate JSON
keys, privileged routes, undeclared scopes, schema constraints, publisher-key
changes, immutable-version conflicts, concurrent registration, semantic collisions,
pagination, current/admission grant ceilings, stale observations, uninstall and
reinstall, and the account SDK RPC path. No developer database is reset.
