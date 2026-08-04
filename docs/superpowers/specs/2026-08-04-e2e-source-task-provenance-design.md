# E2E source-task provenance design

## Purpose

Align the Python E2E harness with the already-merged Multica source-task API. The harness must preserve the durable source identity returned by the server and use it to dispatch a scratch SWE-Lego issue without resending mutable inline issue content.

## Scope

Only the E2E Python client and a focused unit-test module change:

- extend `DispatchHandle` with optional `source_task_id` and `run_id` fields parsed from the first rollout;
- add `MulticaClient.register_source_task(task_type, payload)`, which posts to `/api/v1/source-tasks`, accepts 200/201, and rejects missing or invalid response IDs;
- let `dispatch_scratch(issue_spec, source_task_id=None)` send `source_task_id` and omit `issue` whenever a source ID is supplied; preserve legacy inline issue dispatch when it is absent;
- add hermetic unit tests with a mocked `_request` boundary for response parsing, registration success/failure, and source-only scratch payload construction.

The chain orchestration, branch/resume dispatches, server API, and unrelated diagnosis work are explicitly out of scope.

## Design decisions

`source_task_id` is the only reusable sample identity. `run_id` is response provenance only and is never reused for sampling. Source registration is explicit rather than implicit in `dispatch_scratch`, so callers decide when a payload becomes durable. Optional fields keep existing E2E handle construction and legacy callers compatible.

The tests mock only the HTTP transport boundary and assert client-visible request/response contracts. They do not need a deployed sandbox or Multica service.

## Validation and integration

Run the focused new unit tests and the complete hermetic E2E unit suite, then syntax-check and patch-hygiene-check the changed files. Submit a dedicated PR from a clean worktree to `LRM-Teams/multica:dev`. Merge only after required online CI checks pass.
