# Diagnosis run operations

Runbook for operating env-dispatch diagnosis runs (spec 005). Diagnosis runs
execute in a dedicated per-run sandbox by default
(`DIAGNOSIS_EXECUTION_MODE=sandbox`).

## Poll a run

```bash
curl -H "Authorization: Bearer <PAT-or-JWT>" \
  https://<host>/api/v1/env-dispatch/<projectID>/diagnosis/latest
```

Returns the diagnosis-progress body plus `execution_mode` and
`sandbox_instance_id`. `status` is one of `provisioning`, `running`,
`completed`, `failed`; on failure see `last_error`. 404 means no run exists
yet for the dispatch.

## Failure causes

A failed run's `last_error` carries a classified prefix (capped at 1 KiB):

| Prefix          | Meaning operationally                                                                                   |
| --------------- | ------------------------------------------------------------------------------------------------------- |
| `provisioning:` | Sandbox creation, runtime-online wait, or public-URL resolution failed. Check sandboxd nodes/templates and that `MULTICA_PUBLIC_URL` is reachable from sandboxes. |
| `connectivity:` | The sandboxed agent could not reach the multica diagnosis API. Check egress from sandboxes to `MULTICA_PUBLIC_URL`. |
| `agent:`        | The pi agent itself failed (bad model, crashed, or stopped short of full coverage). Check the pi CLI/model config in the sandbox image. |
| `timeout:`      | The agent exceeded `DIAGNOSIS_AGENT_TIMEOUT_SECONDS`. Raise the timeout or investigate agent stalls.    |
| `extension:`    | Delivering the trusted extension into the sandbox failed. Check the daemon file-ops channel; a missing placeholder falls back to the image-baked extension (warn log only). |
| `enqueue:`      | Delivering the diagnosis task to the sandbox daemon failed. Check daemon connectivity and the agent-inbox pipeline. |

## `run_terminal` 403s

The run-scoped API (`/api/v1/diagnosis-runs/{runID}/...`) authenticates with
the per-run capability token injected into the sandbox. 403 responses:

- `run_terminal` — the run is already `completed`/`failed`; the token is dead
  by design. Expected when an agent retries after finishing or an operator
  replays a captured token. Not an incident.
- Any other 403 — token does not match the run (wrong `runID` or token).
  Tokens are minted per run and only their hash is stored; a lost token cannot
  be recovered, only superseded by re-provisioning (resume re-mints).

## Verify sandbox reclaim

Every terminal run transition enqueues a delete job for the run's
`sandbox_instance_id`. To verify:

1. Logs: `diagnosis sandbox reclaim: reclaim requested` (info, with `run_id`
   and `sandbox_instance_id`) on success; `diagnosis sandbox reclaim: delete
   failed` (warn) indicates a leaked sandbox — delete the instance manually.
2. The sandbox instance/job records for `diagnosis-<runID>` show a completed
   delete; no `diagnosis-*` sandboxes should outlive their run.

## Fall back to server mode

If sandboxed diagnosis is broken fleet-wide:

1. Set `DIAGNOSIS_EXECUTION_MODE=server` on the backend and restart.
2. Expect a deprecation warning in the logs; diagnosis runs in-process with
   the loopback tool server exactly as before this feature, and rewards are
   persisted identically.

The `server` path is deprecated and scheduled for removal — treat the fallback
as temporary and file the sandbox failure cause.
