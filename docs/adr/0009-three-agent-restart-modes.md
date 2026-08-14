# Align Agent Restart with Raft's three reset modes

Agent Restart exposes the same three product modes as Raft v1.0.16:

1. `restart` stops and starts the provider process while retaining its model
   session and complete Agent Workspace.
2. `session` stops the process, clears the model session and context, then
   starts fresh while retaining the Agent Workspace.
3. `full` stops the process, clears the model session and context, resets the
   Agent Workspace, then starts fresh.

None of these modes deletes the server-side Agent identity, configuration,
chat history, or Issues. Full Reset is destructive local reinitialization, not
Agent deletion: it reprovisions the canonical directory and keeps the Agent
executable.

The Web API uses `resetAgent(agentId, mode)` and sends only `{ "mode":
"restart" | "session" | "full" }`. Runtime IDs, filesystem paths, and force
flags are server-owned. The operation response is an `AgentRestartOperation`;
the existing `agent_lifecycle_operation` table remains only as legacy physical
storage so this semantic cut does not require a cosmetic data migration.

Raft Computer 1.0.16 accepts three discrete commands: `agent:stop`,
`agent:reset-workspace`, and `agent:start`. It has no composite
`agent:lifecycle` command. Multica therefore composes each product mode from
the same child primitives:

| Mode | Ordered commands | `agent:start.config` |
| --- | --- | --- |
| `restart` | stop -> start | `{ "sessionId": "<canonical provider session>" }` |
| `session` | stop -> clear canonical session -> start | `{}` |
| `full` | stop -> clear canonical session -> reset-workspace -> start | `{}` |

Raft treats a truthy `config.sessionId` as resume and an absent/empty value as
fresh. Multica follows that behavior directly; it does not expose an invented
nil/empty/omitted three-state contract. Its generic WebSocket envelope remains
`{ "type": "agent:start", "payload": { ... } }`, while the command type and
nested `config.sessionId` semantics match Raft.

Multica retains two stronger correctness proofs around those Raft primitives:

- Stop persists the exact old `launchId` as `stop_launch_id`; only an inactive
  status for that launch may advance the operation.
- Full Reset waits for a terminal reset result carrying the same operation ID
  before dispatching start. Raft's Computer handler alone is fire-and-forget
  and is not sufficient completion evidence for a destructive server-owned
  operation.

Start persists one replacement `launchId` and stable `startDispatchId` before
dispatch. Reconnect replays stop or reset with the same operation ID, while a
starting operation reuses the persisted launch and dispatch IDs through normal
desired/observed Runner reconciliation. Relay publication is not completion:
an undelivered operation stays visible and resumable until Runner Ready or
timeout.

The removed parallel paths must not return: no composite Agent Restart payload
on daemon heartbeat, no `agent:lifecycle` wire command, no restart delivery
lease, no public scheduling/execution mode, and no local
`.multica/lifecycle-commands` ledger.

Agent Restart produces no dedicated toast stream. The direct modal closes once
the request is accepted and normal Agent status/Activity shows the resulting
`Stopped -> Starting -> Idle/Working` facts. Configuration save continues to
use its ordinary save success/failure feedback without an extra restart toast.
