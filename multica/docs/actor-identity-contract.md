# Actor Identity Contract

Activity and Working surfaces must render actor identity from backend snapshots, not client-invented fallback names.

## Backend fields

Every Activity timeline row and agent task item used by Working / active work exposes:

- `actor_id`: UUID of the actor that produced the row.
- `actor_type`: `agent` or `member`.
- `display_name`: non-empty display string supplied by the backend.
- `avatar_url`: optional avatar URL.
- `handle`: optional stable handle / slug when the actor has one.
- `actor_status`: `visible`, `hidden`, or `deleted`.
- `actor`: nested object with the same identity snapshot (`actor_id`, `actor_type`, `display_name`, `avatar_url`, `handle`, `status`) for new clients that prefer a grouped contract.

## Status semantics

- `visible`: the actor is visible to the current viewer. `display_name` is the actor's current/historical backend snapshot.
- `hidden`: the actor exists but is not visible to the current viewer. UI may show the backend-provided safe placeholder (for example `Hidden agent`) and must not try to resolve the directory entry client-side.
- `deleted`: the actor row is archived or no longer resolvable. Backend returns a safe non-empty display value such as `Deleted agent` / `Deleted member` when no better snapshot exists.

## UI requirements

- UI must never display `Unknown Agent`.
- Working / active work must use the backend identity snapshot first.
- If a task/event has no resolvable actor identity and the backend marks it `hidden` or `deleted`, render the provided safe placeholder or omit it from Working when the surface cannot present a safe state.
- Clients may use agent/member directories only as an enhancement; directory miss must not create a user-visible `Unknown Agent` fallback.
