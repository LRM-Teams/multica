# Research Fleet Design (2026-07-29)

## Summary

Workspace module **调研** (`/:slug/research`) runs a sealed Research Fleet led by **罗纳尔多**. Users supply goals only; the fleet executes a multi-source methodology with a live exploration map, weighted sources, structured reports, staged evaluation, and fleet-scoped self-evolution. After user confirmation, optional handoff creates a development project and/or channel.

## Decisions

- Approach A: sealed fleet of real agents + `research_session` entity + exploration graph.
- User chats with 罗纳尔多 by default; @mention for others.
- Hire via Wendy shell → pending_prompt_review → 罗纳尔多 optimize → activate.
- Stage gates S1–S4 only (not per step). Evolution is user-invisible, `research_fleet_only`.
- Web + Desktop parity with sibling modules.

## Surfaces

- Nav + paths + i18n
- API `/api/research/*`
- CLI `multica research *`
- Builtin skill `multica-research-fleet`
- WS `research_session:*` incremental updaters

See implementation plan `docs/superpowers/plans/2026-07-29-research-fleet.md`.
