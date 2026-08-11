# Members Directory replaces the Agents page

**Status:** accepted (2026-08-11, grilled with product owner)

The workspace primary surface for browsing people and Agents is the **Members Directory**, not a separate Agents dashboard. The sidebar label and web route use **Members** (`/:slug/members`). The former Agents list page, `/:slug/agents/:id` management page, and Settings **Members** tab are removed from the product; human invite, role change, and remove live only on the Members Directory. Agents remain a distinct domain entity (execution, presence, Agent Root); they appear in the directory as members-of-the-workspace catalog entries, not as a second product IA.

## Why

- Raft-style IA treats humans and Agents as one roster with per-kind profile panels.
- Keeping Settings members admin, Agents list, and agent detail as three places for “who is in this workspace” produced split ownership and stale docs (`members-roles` still pointed at Settings invite).
- Unifying user-visible copy and routes on **Members** without pretending Agent and human are one storage type.

## API naming

New user-facing HTTP resources for this surface hang under a **`members`** path prefix (exact paths chosen at implementation). `packages/core` switches to those paths for Web. Legacy **`/api/agents`** remains as a server alias for **mobile**, **CLI**, and unupgraded clients; those clients are out of scope for UI work in the first cut. Desktop is not a separate project: it shares core/views and picks up changes on rebuild without dedicated desktop QA in v1.

## Deliberate non-goals (v1)

- Graph row in the left rail
- Pending invitation list / revoke / resend
- Workspace-wide permanent join link (email invite only; single address + role)
- Hire/draft query deep links (`?action_card=`, `?draft=`) auto-opening create
- Showing archived Agents in the left rail
- Mobile Members Directory UI
- Deleting legacy `/api/agents` in the same change

## Consequences

- Product docs (`members-roles`, agents nav) must describe Members Directory as the human-admin and agent-browse entry.
- Agent profile and config continue through **Agent Side Panel** (page variant); human profile through **Member Side Panel**, extended with role edit and remove.
- Redirects from `/agents` and `/agents/:id` preserve bookmarks.

See also: `docs/members-directory-decisions.md` (decision table from the grill session).
