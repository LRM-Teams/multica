# Autopilots source map

- `server/cmd/multica/cmd_autopilot.go` registers `list`, `get`, `create`, `update`, `delete`, `trigger`, `runs`, `trigger-add`, `trigger-update`, `trigger-delete`, and `trigger-rotate-url`.
- The CLI maps reads/writes to `/api/autopilots`, `/api/autopilots/{id}`, `/api/autopilots/{id}/trigger`, `/api/autopilots/{id}/runs`, and trigger subroutes.
- `server/internal/service/autopilot.go` has `DispatchAutopilot`, creates `autopilot_run`, and switches on `execution_mode`.
- `create_issue` calls `dispatchCreateIssue`; `run_only` calls `dispatchRunOnly`.
- `resolveAutopilotLeader` resolves squad-assigned autopilots to the squad leader.
- `AgentReadiness` blocks archived/runtime-unready agents before enqueue.
- `server/cmd/server/router.go` exposes authenticated `/api/autopilots` routes and unauthenticated webhook ingress `/api/webhooks/autopilots/{token}`.
- `SyncRunFromTask` (`server/internal/service/autopilot.go`) syncs a `run_only` run to `completed`/`failed` from its task and publishes `EventAutopilotRunDone`.
- `notifyCreatorOnAutopilotRunDone` (`server/cmd/server/autopilot_listeners.go`) subscribes to `EventAutopilotRunDone` and, on `completed` runs with non-empty output, writes an `autopilot_run_completed` inbox item to the creator (via `resolveAutopilotPausedRecipients`) carrying the run's final output as the body — so `run_only` results reach the creator without an issue. `create_issue` runs (empty task `Result`) are skipped; failures are left to the failure-rate auto-pause monitor.
