# Research Run V6 HTTP and realtime contract

Status: target transport contract frozen; production disabled.

Authority: [`superpowers/specs/2026-08-14-ronaldo-research-director-development-spec.zh-CN.md`](superpowers/specs/2026-08-14-ronaldo-research-director-development-spec.zh-CN.md), [`research-run-v6-contract.md`](research-run-v6-contract.md), and [`research-run-v6-storage-contract.md`](research-run-v6-storage-contract.md).

This document fixes the Web/Desktop, Agent and Report-origin transport. It does
not make HTTP or realtime payloads canonical research state.

## 1. Common rules

- Workspace authorization follows existing authenticated middleware. User routes
  require a user with access to the Run's Workspace.
- Agent routes require the task-scoped Agent principal. The credential must match
  workspace, Run team membership, Work Item, Attempt, Inbox delivery and Manifest.
- JSON requests reject unknown fields and trailing values. Maximum body sizes are
  endpoint-specific and checked before decoding.
- Every write carries a UUID `client_request_id`. Repeating the same request ID
  and canonical payload hash returns the original committed outcome; a different
  hash returns `research.v6.idempotency_conflict`.
- Mutable commands carry `expected_state_version` or a frozen input hash. HTTP
  success never overrides the transaction's identity/version checks.
- UUIDs, revisions, hashes and event sequences are serialized as JSON strings or
  integers exactly as defined by the target Schema; clients do not derive them
  from titles or array positions.

## 2. Response and error envelopes

New V6 write endpoints return:

```json
{
  "client_request_id": "00000000-0000-4000-8000-000000000001",
  "outcome": "accepted",
  "replayed": false,
  "state_version": 13,
  "through_event_sequence": 48,
  "refs": [
    { "kind": "work_item", "id": "00000000-0000-4000-8000-000000000002" }
  ]
}
```

`outcome` is `accepted | partially_rejected | rejected`. A Director Proposal may
return `action_results[]` with `action_id`, `outcome`, stable `code` and produced
refs. A rejected prerequisite marks its dependents `dependency_rejected`; the
server does not execute them.

Errors preserve the repository's `error` field and add stable V6 fields:

```json
{
  "code": "research.v6.stale_input",
  "error": "one or more input versions already have a successor",
  "retryable": true,
  "current_refs": [
    { "kind": "insight", "id": "00000000-0000-4000-8000-000000000003", "revision": 2 }
  ]
}
```

| HTTP | Code | Meaning |
| --- | --- | --- |
| 400 | `research.v6.invalid_contract` | strict decode, second validator, hash or semantic shape failed |
| 401 | existing auth code | no valid user/Agent principal |
| 403 | `research.v6.principal_mismatch` | principal does not own this workspace/Run/Attempt/Manifest |
| 404 | `research.v6.not_found` | scoped entity does not exist or is not visible |
| 409 | `research.v6.state_version_conflict` | expected state/version changed |
| 409 | `research.v6.brief_incomplete` | Director has not acknowledged every frozen Brief page |
| 409 | `research.v6.stale_input` | an input is absorbed, superseded or no longer fresh |
| 409 | `research.v6.idempotency_conflict` | request ID was reused with a different canonical hash |
| 409 | `research.v6.team_limit` | adding a membership would exceed 50 |
| 409 | `research.v6.director_unavailable` | Run is `awaiting_director` for a Director-only action |
| 409 | `research.v6.invalid_transition` | current lifecycle state forbids the operation |
| 413 | `research.v6.payload_too_large` | request or upload exceeds the configured hard limit |
| 422 | `research.v6.upload_mismatch` | stored object MIME/size/hash/path differs from declaration |
| 503 | `research.v6.capability_unavailable` | required model, Agent runtime, storage or renderer is unavailable |

Internal diagnostics, provider messages, object keys and credentials never enter
the client error envelope. `retryable` describes replay safety, not a promise that
the same stale payload will later succeed.

## 3. User routes

### 3.1 Create a V6 Run

`POST /api/research/sessions`

The existing create body gains these V6 fields:

```json
{
  "orchestrator_version": "research-run-v6",
  "director_agent_id": "00000000-0000-4000-8000-000000000004",
  "client_request_id": "00000000-0000-4000-8000-000000000005"
}
```

For V6, `director_agent_id` is required and must name a non-archived Agent in the
same Workspace. The create transaction writes the Run, initial Contract revision,
Director assignment and exactly one Run team membership. It leaves legacy
`fleet_id` null and creates no other Agent or fixed role.

### 3.2 Replace or restore the Director

`PUT /api/research/sessions/{id}/director`

```json
{
  "director_agent_id": "00000000-0000-4000-8000-000000000014",
  "expected_state_version": 13,
  "reason": "用户选择新的调研负责人。",
  "client_request_id": "00000000-0000-4000-8000-000000000015"
}
```

Only a user can call this route. Selecting the same unavailable Director records
an explicit recovery generation; selecting another Agent closes the old
assignment and creates a new generation. Accepted facts and in-flight commit
reconciliation remain attached to the Run.

### 3.3 Send natural-language steering

`POST /api/research/sessions/{id}/messages`

The existing message body gains optional `selected_research_refs`:

```json
{
  "content": "这个方向证据不够，继续补充低配实例测试。",
  "client_request_id": "00000000-0000-4000-8000-000000000021",
  "selected_research_refs": [
    {
      "stable_id": "insight:00000000-0000-4000-8000-000000000306",
      "kind": "insight",
      "entity_id": "00000000-0000-4000-8000-000000000306",
      "revision": 1,
      "content_hash": "sha256:cccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccc",
      "display_summary": "并发负载下的延迟边界"
    }
  ]
}
```

The server stores the raw message and exact refs first, then creates a Steering
Work Item through the durable outbox. Missing, stale or invisible refs are marked
in the Assessment input; they do not silently retarget by title. The user never
sends merge, stop, replan or tier commands.

## 4. Agent routes

### 4.1 Fetch a frozen Manifest

`GET /api/agent/research/sessions/{id}/work-items/{workItemId}/attempts/{attemptId}/manifest`

Returns one strict `work_manifest` envelope. `ETag` is the Manifest hash. The
server returns 409 if the Attempt is no longer executable and 403 if the current
task credential is not bound to it. A successful retry returns identical bytes.

### 4.2 Review a paged Director Brief

For a Director Work Item:

- `GET /api/agent/research/sessions/{id}/work-items/{workItemId}/attempts/{attemptId}/director-brief?cursor=<opaque>`
- `POST /api/agent/research/sessions/{id}/work-items/{workItemId}/attempts/{attemptId}/director-brief-acks`

GET returns one strict `director_brief` page. ACK requires
`client_request_id`, `brief_id`, `brief_hash`, `page_key` and `page_hash` and is
idempotent. With no cursor, a restarted Director receives the first
unacknowledged page. Action Proposal submission fails with
`research.v6.brief_incomplete` unless every page in the frozen Brief manifest is
acknowledged by the same Director generation. The server may carry a prior
acknowledgment into a new Brief only for an identical page hash and Director
generation; changed pages remain unacknowledged.

### 4.3 Traverse the authorized catalog

`GET /api/agent/research/sessions/{id}/work-items/{workItemId}/attempts/{attemptId}/catalog?view=same_tier|higher_candidates&cursor=<opaque>`

The server ignores any client-supplied tier or Branch outside the Manifest's
`catalog_access`. `same_tier` pages every fresh nonterminal Result/Insight of the
authorized tier across the Run. `higher_candidates` pages only higher nodes bound
to an authorized Branch. Items contain stable ID, exact version/hash, tier,
Branch IDs, catalog summary, scope summary, state and Steward ID; they do not
contain full content.

Every page is pinned to `through_event_sequence` and returns `page_hash`,
`page_key`, `next_cursor` and `has_more`. GET is read-only. After the Agent has
processed the page it calls:

`POST /api/agent/research/sessions/{id}/work-items/{workItemId}/attempts/{attemptId}/catalog-acks`

```json
{
  "client_request_id": "00000000-0000-4000-8000-000000000029",
  "page_key": "same-tier:47:1",
  "page_hash": "sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
}
```

The idempotent acknowledgement records the Attempt's review watermark. With no
cursor, a restarted Agent receives its first unacknowledged page. After it
exhausts the pinned catalog, later new nodes appear in a new pinned page set and
do not reorder acknowledged pages. Cursor, page hash and Manifest mismatch fail
closed.

### 4.4 Submit typed work

`POST /api/agent/research/sessions/{id}/work-items/{workItemId}/attempts/{attemptId}/submission`

The request is exactly one of:

- `director_action_proposal`
- `atomic_result_submission`
- `discussion_turn_submission`
- `integration_submission`
- `report_package_submission`

`director_brief`, `work_manifest`, `projection_snapshot` and `projection_delta`
are server-produced and are rejected on this endpoint. The Work Item's
`expected_result_schema` chooses the allowed root type; the body cannot choose a
more permissive decoder. Maximum JSON body size is 2 MiB except Report package
metadata, which remains 2 MiB because bytes use upload sessions.

### 4.5 Upload Report package inputs

`POST /api/agent/research/sessions/{id}/work-items/{workItemId}/attempts/{attemptId}/report-uploads`

```json
{
  "client_request_id": "00000000-0000-4000-8000-000000000031",
  "path": "index.html",
  "role": "document",
  "media_type": "text/html;charset=utf-8",
  "byte_size": 24576,
  "content_hash": "sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
}
```

The server normalizes and validates the path, allocates its own immutable object
key and returns an upload ID plus either a presigned destination or the existing
local upload destination. Object keys are never accepted from the Agent.

`POST /api/agent/research/sessions/{id}/work-items/{workItemId}/attempts/{attemptId}/report-uploads/{uploadId}/complete`

```json
{
  "client_request_id": "00000000-0000-4000-8000-000000000032"
}
```

Completion re-reads or heads the object and compares MIME, size and SHA-256 with
the frozen declaration. Only completed upload resource IDs may appear in a
`report_package_submission`. Upload capabilities expire and cannot be moved to a
different Work Item Attempt.

## 5. Projection reads

### 5.1 Snapshot and Slice

- `GET /api/research/v6/runs/{runId}/projection/snapshot?cursor=<opaque>`
- `GET /api/research/v6/runs/{runId}/projection/slice?root=<stableId>&depth=1&cursor=<opaque>&snapshot_id=<uuid>`

Both return a strict `projection_snapshot`. A Slice uses a non-default
`slice_key`; every page keeps the same `snapshot_id`,
`through_event_sequence` and `projection_hash`. Cursors are opaque, scoped to
workspace/Run/snapshot/slice parameters and rejected if altered or reused across
users.

`depth` is fixed to 1 for derivation expansion in V6. Viewport density reads may
use server-defined bounded rectangle/zoom parameters, but they cannot change
canonical visibility or create graph nodes.

### 5.2 Delta page and resume

`GET /api/research/v6/runs/{runId}/projection/deltas?after=<eventSequence>&cursor=<opaque>`

```json
{
  "run_id": "00000000-0000-4000-8000-000000000003",
  "deltas": [],
  "next_cursor": null,
  "resync_required": false
}
```

Each item is a strict `projection_delta`; event sequences are contiguous and the
previous/new projection hashes form a chain. If the first retained sequence is
newer than `after + 1`, the response contains no deltas and
`resync_required=true`.

`POST /api/research/v6/runs/{runId}/projection/resume`

```json
{
  "snapshot_id": "00000000-0000-4000-8000-000000000601",
  "last_confirmed_sequence": 47,
  "projection_hash": "sha256:dddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddd"
}
```

Returns the same Delta-page envelope or `resync_required=true`. Snapshot ID,
sequence and hash must agree; sequence alone is insufficient.

### 5.3 Node detail

`GET /api/research/v6/runs/{runId}/projection/nodes/{nodeId}?view=brief|full|history`

The response contains stable/canonical refs, current three-dimensional state,
reason detail, Agent/Task/Attempt, Branches, evidence refs, Discussion refs,
successor/history refs and Report refs permitted for the user. Default `brief`
does not inline full source snapshots or Discussion transcripts. `full` and
`history` are paginated and return exact Artifact Version IDs/hashes.

## 6. Report metadata

- `GET /api/research/v6/runs/{runId}/reports`
- `GET /api/research/v6/runs/{runId}/reports/{reportId}`

List returns immutable revision metadata, lifecycle/review status, author,
Director review, input counts, package/document hashes and publish time; it never
inlines HTML or object keys. Detail adds exact input refs, outline, citations,
plain-text fallback, render diagnostics and one `sandbox_url` only when the user
may view that revision.

Latest published is the default Goal attachment. Draft and published
`sandbox_url` values are short-lived signed bearer capabilities. Their path
contains the immutable Report ID and package hash; expiry/signature query values
may rotate without changing document identity. The Report origin validates that
capability and receives no main-app Cookie or credential. Knowing a Report ID or
package hash alone does not grant access.

## 7. Realtime

The authenticated existing realtime bus publishes:

```json
{
  "event": "research_projection_v6:delta",
  "payload": {
    "run_id": "00000000-0000-4000-8000-000000000003",
    "delta": {}
  }
}
```

`delta` is a strict `projection_delta`. Clients ignore other Runs, apply events
only in contiguous sequence/hash order and call resume after reconnect. Malformed
frames, a gap timeout, snapshot mismatch or hash mismatch cause a full Snapshot
reload; clients never repair canonical state locally.

User-visible control changes that do not alter graph content may still emit an
empty Delta with a new sequence and hash chain. Notifications such as
`awaiting_director` use existing user notification transport and also appear in
the next Projection/Run metadata read.

## 8. Report origin

The configured `RESEARCH_REPORT_ORIGIN` must differ from every main Web/Desktop
application origin. Startup and V6 activation fail closed if it is missing,
equal to the app origin, sends app cookies, accepts an invalid/expired signed
capability or cannot apply the required headers.

Published HTML responses include at least:

```text
Content-Type: text/html; charset=utf-8
Content-Security-Policy: sandbox allow-scripts; default-src 'none'; script-src <server hashes>; style-src <server hashes>; img-src data:; font-src data:; connect-src 'none'; object-src 'none'; frame-src 'none'; worker-src 'none'; media-src 'none'; base-uri 'none'; form-action 'none'; frame-ancestors <configured app origins>
Referrer-Policy: no-referrer
X-Content-Type-Options: nosniff
Cross-Origin-Resource-Policy: cross-origin
Cache-Control: private, max-age=300, immutable
ETag: "<document-content-hash>"
```

The embedding application also sets exactly `sandbox="allow-scripts"` on the
iframe. No `allow-same-origin`, forms, popups, downloads, top navigation,
WebView/preload, credentialed CORS or generic `postMessage` bridge is permitted.
The compiled document has no runtime subresource request. Closing or watchdog
termination removes the iframe.

## 9. Compatibility

- Existing V1–V5 routes and response shapes remain unchanged.
- Existing Agent task-result endpoint continues to serve V1–V5; V6 uses the
  generic Work Item submission endpoint.
- Existing Markdown Report reader remains available for legacy revisions.
- Web/Desktop select the transport by the Run's pinned orchestrator version.
- Until activation succeeds, attempts to create a production V6 Run return the
  existing unsupported-version response and none of these routes authorize V6
  mutation.
