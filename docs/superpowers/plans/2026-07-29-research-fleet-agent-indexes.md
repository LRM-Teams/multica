# Research Fleet agent foreign-key indexes

## Goal

Restore the repository invariant that every foreign key involved in Agent hard
delete has a supporting child index.

## Evidence

- PR #1375's backend CI failed after migration `244_research_fleet`.
- The invariant tests reported exactly three missing Agent-reference indexes:
  `research_fleet.lead_agent_id`, `research_graph_node.actor_agent_id`, and
  `research_message.target_agent_id`.
- PR #1375 changes only frontend voice-call files and cannot create database
  constraints.
- Migration `244_research_fleet` introduced all three nullable foreign keys
  without indexes.

## Checklist

- [x] Read the failing GitHub Actions job and isolate the exact constraints.
- [x] Confirm the failure is unrelated to the voice-call PR.
- [x] Add an idempotent concurrent-index hook after the Research Fleet tables
  exist.
- [x] Add a reversible marker migration for already-upgraded databases.
- [x] Run migration package tests against PostgreSQL in CI.
- [x] Push an independent repair PR before retrying the voice PR.

## Boundary

The repair adds only the three missing indexes. It does not change Research
Fleet data, foreign-key actions, or Agent deletion behavior.
