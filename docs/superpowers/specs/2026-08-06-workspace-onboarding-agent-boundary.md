# Workspace Onboarding Agent capability boundary

Date: 2026-08-06
Status: confirmed design; implementation not started

## Outcome

Multica gives each configured Workspace one privileged Onboarding Agent,
created with the default display name Wendy. The role is identified only by
`workspace.onboarding_agent_id`. Human management remains available outside
Wendy, while no ordinary Agent receives or can exercise Workspace hiring
capability.

## Setup lifecycle

Workspace creation and Wendy creation are separate boundaries:

1. Create the Workspace and its sole Owner.
2. Gate normal Workspace entry on mandatory setup.
3. Connect at least one Computer and obtain an online Runtime.
4. The Owner selects Wendy's Runtime and Model and explicitly chooses
   `Create Wendy`.
5. One transaction creates or adopts Wendy, binds
   `workspace.onboarding_agent_id`, binds the platform-owned hiring skill,
   adds Wendy to `#general`, writes the versioned deterministic onboarding
   messages, and marks this setup step complete.
6. Runtime/provider work after the commit is asynchronous. A failed setup
   transaction leaves the Workspace intact and the setup step retryable.

There is no user-visible Agent without a Runtime and no implicit Runtime or
Model selection. The setup experience may follow Raft's Computer-first,
explicit Cindy-creation flow, but the contract above is Multica-owned.

## Identity and lifecycle

- The binding is the only authority. Renaming Wendy does not change her role;
  renaming another Agent to Wendy grants nothing.
- Wendy is Workspace-visible, joins `#general`, and posts deterministic welcome
  content there. Later personalized conversation comes from the real Runtime.
- The Workspace has exactly one Owner. This design does not add an ownership
  transfer flow or permit a second Owner through general role editing.
- Owner and Admin may edit Wendy's instructions, optional skills, Runtime, and
  Model. The platform-owned hiring skill cannot be removed while Wendy is active.
- Only the sole Owner may perform initial Wendy setup, archive Wendy, or restore
  Wendy. Admin cannot perform those lifecycle actions.
- Archive is the existing soft-delete behavior. It preserves
  `onboarding_agent_id`, immediately disables hiring, creates no replacement,
  and permits the Owner to restore the same Agent.
- There is no first-version entry to designate an arbitrary existing Agent as
  the Onboarding Agent.

## Knowledge and authorization boundary

- Wendy receives the hiring persona, `multica-creating-agents`, and the command
  contract needed to prepare Hiring Proposals.
- Ordinary Agents continue receiving general work skills but receive no hiring
  skill, prompt text, CLI teaching, help text, or API instructions.
- The Agent-facing prepare endpoint authorizes the authenticated Agent ID only
  when it equals the Workspace's active `onboarding_agent_id`; other Agents get
  `403` even if they construct the request without instructions.
- Research Fleet member creation remains a separate, lead-scoped capability and
  is not Workspace hiring.
- Hiring cards may appear in a shared group. Members, including Agents, may see
  the shared fact; visibility does not inject the protocol or grant authority.
- An Agent request cannot use Wendy as a confused deputy to trigger hiring.
  Human Members may ask Wendy for a proposal; only Owner/Admin can commit it.
- Owner/Admin retain the direct human Create Agent UI and need not go through
  Wendy.

## Hiring Proposal lifecycle

1. Wendy performs only the necessary intake and prepares a structured
   `agent:create` Hiring Proposal in the originating human conversation.
2. The card remains `prepared` until an Owner/Admin commits or an authorized
   human dismisses it.
3. Commit locks the card and atomically creates the Agent plus transitions the
   card from `prepared` to `done`, recording actor, Agent, and time.
4. Concurrent or repeated commits cannot create another Agent. Every losing
   request receives a terminal conflict response.
5. A completed card remains visible as audit history, links to the created
   Agent, and exposes no active Create button.
6. Card terminal-state changes invalidate or update every client projection.

When structured action-card Parts exist, UI surfaces do not render protocol
fallback content such as `[agent:create proposal] <name>`. Compatibility text
may remain in canonical content for old clients, but current renderers suppress
it in favor of the structured card.

## Existing Workspaces

Migration begins with read-only counts and never merges, deletes, or silently
chooses among ambiguous Agents.

- Already bound: preserve the binding.
- Unbound with exactly one eligible legacy Wendy: bind that Agent without
  changing its Runtime or Model.
- Unbound with no Wendy: require the Owner to complete Wendy setup.
- Unbound with multiple candidates: record the conflict and require explicit
  repair; do not choose by name ordering.

After binding, no runtime lookup, permission check, event route, UI badge, or
notification may infer the role from Wendy's name.

## Required executable controls

- DB constraints for same-Workspace binding and exactly one Workspace Owner.
- Setup transaction rollback and idempotent retry tests.
- Object-bound `403` tests for ordinary Agent credentials, including renamed
  Wendy and ordinary Agents named Wendy.
- Archive/restore tests proving Owner success and Admin/Member/Agent denial.
- Skill and prompt snapshot tests proving hiring knowledge is absent from
  ordinary Agent execution profiles.
- Hiring Proposal concurrency tests proving one card creates at most one Agent.
- UI tests for completed cards, cross-client invalidation, and fallback-content
  suppression using the original reported string.

## Not in this design

- Autonomous Agent hiring.
- Assigning an arbitrary replacement Onboarding Agent.
- Owner transfer UI.
- Hiding shared Hiring Proposal history from Agents who are channel members.
- Implementing or fixing the behavior in this documentation change.
