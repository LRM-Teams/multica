# Research FE Mock Layer (LRM-841)

Typed, deterministic mocks for the research module so FE slices (819–828) can
build the four UI states without waiting on the live fleet.

## Exports

From `@multica/core/research`:

| Export | Purpose |
|---|---|
| `researchMocks.snapshots.default` | Populated session snapshot (nodes / edges / sources / report / evals / wake_failed process card) |
| `researchMocks.snapshots.empty` | First-visit state (no graph / sources / report) |
| `researchMocks.snapshots.loading` | Session row only — used for skeleton rendering |
| `researchMocks.snapshots.error` | Running session + `wake_failed` process card (`meta.op` / `meta.reason`) |
| `researchMocks.snapshots.clarification` | Running session + `clarification_question` list/form process cards (LRM-822) |
| `researchMocks.snapshots.awaitingConfirm` | `awaiting_user_confirm` + S4 delivery (LRM-840 approve/reject chrome) |
| `researchMocks.lists.default` / `.empty` | Session list payloads |
| `researchMocks.createResponse` | Kickoff payload shape for create-session flows |
| `researchMocks.api` | Explicit mock surface mirroring `api.*Research*` signatures — wire in via query-client override or test harness, **never** silently in prod |

## Type / contract notes

- Shapes come from `@multica/core/types/research` (already includes 11 node
  types, `confidence` projection, `wake_failed` process meta, and the LRM-843
  report `structured` v1).
- Report fixture reused from `docs/research/fixtures/report-v1.example.json`
  semantics — `structured.schema_version === 1`.
- No runtime fallback: consuming code passes the mock explicitly (tests,
  storybook, dev harness). Production query functions continue to call the real
  API.

## State coverage

| UI slice | Mock state |
|---|---|
| 819 stage timeline | `snapshots.default` (`current_stage` + stage_gate node) |
| 820 streaming / stop | `snapshots.default` + WS updaters |
| 821 source citation card | `snapshots.default.sources` + report citations |
| 823 interruption banner | `snapshots.error` (`wake_failed`) |
| 822 clarification options/form | `snapshots.clarification` (`clarification_question`) |
| 840 stage gate approve/reject | `snapshots.awaitingConfirm` (`awaiting_user_confirm`) |
| 825 canvas empty / first load | `snapshots.empty` / `snapshots.loading` |
| 826 node detail drawer | `snapshots.default.nodes` + sources |
| 828 dead-end retry | `snapshots.error` + `dead_end` node |
