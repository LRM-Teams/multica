# Harness boundary: Multica kernel vs execution shell

Status: productized (LRM-955)  
Related Spec: Scope / Memory / Skill / Harness (agent notes)

## Kernel (not swappable)

These semantics stay Multica's regardless of which coding harness runs a turn:

- Issue status machine and assignment
- Goal mode and channel goal checkpoints
- Channel / DM membership and permissions
- Group manager orchestration
- Daemon claim / inbox / delivery leases
- Audit and platform CLI contracts (`multica …`)

Changing Codex ↔ Claude ↔ Pi (or similar) must **not** require rewriting these.

## Shell (swappable)

- Coding harness / provider CLI
- Model and thinking-level selection
- Provider-native session resume
- Research / specialty backends behind the same Multica task surface

## Same-machine runtime switch — memory continuity

| Asset | Follows | After switch |
| --- | --- | --- |
| Multica agent memory tree (`MULTICA_AGENT_ROOT`) | Agent ID | **Preserved** (one authoritative root per agent on the machine) |
| Provider session | Task / runtime | May discard and rebuild |

Rules:

1. Agent root and working directory are `~/.multica/workspaces/<workspace_id>/agents/<agent_id>` — never keyed by task, runtime, or provider.
2. Multiple runtimes may coexist; durable writes go to the same Agent root.
3. Harness-private caches are scratch paper, not canonical memory.
4. Scope layering (member / channel / project / agent) is unchanged by a harness swap.

## Acceptance / regression

- Unit: `TestMulticaAgentRootStableAcrossHarnessSwitch` in `server/internal/daemon`.
- Manual: bind the same agent to two providers on one daemon; write a durable note under `$MULTICA_AGENT_ROOT/memory`; switch provider; confirm the note is still present at the same path and `MULTICA_AGENT_ROOT` is unchanged.
