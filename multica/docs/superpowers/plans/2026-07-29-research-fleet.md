# Research Fleet implementation plan

## Goal

Ship production Research Fleet module: sealed squad, exploration map UI, multi-source report, stage eval, handoff.

## Status

Implemented on branch `agent/research-fleet`:

- Migration `244_research_fleet`
- Handler/API/CLI/skill + domain playbooks (tech/market/academic)
- Session wake via `EnqueueChatTask` (`research_dispatch.go`)
- Core schemas/queries/WS updaters (incl. presence cache)
- Views list + session three-column UI + exploration map
- Web + Desktop routes, sidebar, locales
- Research fleet agents hidden from ListAgents / assignee picker

## Verification

```bash
cd server && go test ./internal/handler/ -run 'Research|BuildResearch|NextResearch' -count=1
pnpm --filter @multica/core exec vitest run research paths
pnpm --filter @multica/views exec tsc --noEmit
pnpm react:doctor
```

## Commands run / notes

- sqlc research queries generated in isolation (manual-only sqlc files left untouched)
- Go research handler tests + core vitest + react:doctor (changed scope) green
- Local full product interaction not run; CI + deploy evidence required for “可用”

## Boundaries

- Fleet agents still need a workspace runtime to seed
- Stage evaluator is rule-based gate (LLM evaluator can plug into same API)
- Wendy hire path is lead API create; UI does not require user to open Wendy
