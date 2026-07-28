# #801 alias-zero residual runbook

When `scripts/measure-agent-human-route-hits.sh` prints **RESIDUAL**, use this path.
Do not invent process at deploy time.

Metric: `multica_agent_surface_human_route_hits_total{site="…"}`

## 1. Read the residual

```bash
# Prefer Prometheus time window
PROM_URL=… WINDOW=1h ./scripts/measure-agent-human-route-hits.sh

# Or process-lifetime counters on served /metrics
METRICS_URL=https://served/metrics ./scripts/measure-agent-human-route-hits.sh
```

Note every non-zero `site=` value and its increase/value.

## 2. Map `site=` → handler / middleware

| site label | Meaning | Code |
|---|---|---|
| `RejectAgentOnHumanAPI` | AgentPrincipal hit **any** path outside `/api/agent/*` (global middleware 403) | `middleware.RejectAgentOnHumanAPI` in `server/internal/middleware/principal.go`; wired in `cmd/server/router.go` protected group |
| `ListChannels` | Human `GET /api/channels` with agent token | `handler.ListChannels` |
| `ListChannelMembers` | Human channel members list | `handler.ListChannelMembers` |
| `loadAttachmentForRequest` | Human attachment metadata path | `handler.loadAttachmentForRequest` |
| `loadAttachmentForDownload` | Human attachment download path | `handler.loadAttachmentForDownload` |
| `loadIssueForUser` | Human issue loader (any human issue route that still ran) | `handler.loadIssueForUser` |
| `loadProjectForResource` | Human project resource loader | `handler.loadProjectForResource` |

If `site=RejectAgentOnHumanAPI` dominates, residual is **not** a thin alias residual — something is still calling human/admin URLs (CLI dual-path miss, old binary, or raw HTTP). Use access logs (next section) for the path.

## 3. Locate which runtime / agent credential

The counter is intentionally **low-cardinality** (site only). It does **not** label `agent_id`. To attribute:

1. **Time-correlate** residual window with server access logs / reverse-proxy logs for status **403** and body containing `agent must use dedicated /api/agent/* route` (or HTTP status 403 on human routes with `Authorization: Bearer mat_…`).
2. Log fields that usually exist on Multica API:
   - `Authorization` prefix `mat_` (never log full token)
   - `X-Workspace-ID` / workspace slug
   - Request path + method
   - Daemon / runtime host if present on daemon-proxied paths
3. **Resolve agent from credential** (ops, private DB or admin tools — not public chat):
   - Hash token → `agent_credential` / task token / inbox token tables
   - Map to `agent_id`, `workspace_id`, optional task/inbox event
4. On the agent host: `multica` / daemon version (`multica version`, daemon log banner). Residual often tracks **old CLI** still shipping dual-path bugs.

If logs lack mat_ attribution, add a **temporary** structured log on `RejectAgentOnHumanAPI` / `rejectAgentOnHumanRoute` with `agent_id`, `workspace_id`, `path`, `site` (no token) — then remove after incident. Do not expand Prometheus labels with agent_id (cardinality).

## 4. Top three causes — confirm and fix

### A. Old CLI / daemon not upgraded

**Confirm:** residual agents show CLI/daemon version **before** the #801 cut tips (`ec4cd0689` / PR #1287). Path in logs is human `/api/issues…` or `/api/channels` while fleet already has dedicated routes.

**Fix:** fleet upgrade daemon+CLI; re-run measure after soak. No server code change.

### B. CLI dual-path miss (branch not switched for mat_*)

**Confirm:** current tip CLI still has a hard-coded `/api/…` human path on a code path agents still use (grep `cmd/multica` for `/api/issues`, `/api/channels`, `/api/attachments`, `/api/projects` without `isAgentAPIToken` / `agentIssueAPIPath`). Access log path matches that command.

**Fix:** add mat_* switch to dedicated `/api/agent/*` (or stop offering the command for agents). Add a Vera path contract test so it cannot regress green on human path.

### C. Caller bypasses CLI (raw HTTP / old SDK / script)

**Confirm:** User-Agent / caller is not `multica` CLI; or automation uses human PAT incorrectly with agent identity; or test harness hits human routes with mat_*.

**Fix:** point caller at `/api/agent/*`; for true admin ops use human token, not agent credential. If intentional probe of 403, residual is expected only during that probe — exclude from “done” window.

## 5. After fix

1. Re-run `measure-agent-human-route-hits.sh` with the same WINDOW.
2. Target: **VERDICT: ZERO**.
3. Wire into Vera’s #801 acceptance runner as criterion ② (call this script; do not reimplement).

## 6. Ongoing (not one-shot)

Any residual after merge is a **regression of the safety boundary**, not noise. Keep the measure script + this runbook next to the acceptance runner so any eng can answer “is #801 still holding?” without Ronan present.
