# Workspace Onboarding Agent capability boundary

Date: 2026-08-06
Status: ready for implementation

## Problem Statement

Every Multica Agent currently receives the platform's Agent-creation knowledge,
and any authenticated Agent can prepare an `agent:create` action card. This
makes recruiting part of every worker Agent's prompt and effective capability,
even though a Workspace already has a dedicated onboarding Agent, Wendy. It
increases irrelevant context, permits accidental or adversarial fleet growth,
and makes a display-name convention carry responsibilities that should be an
explicit Workspace relationship.

The current onboarding sequence also creates Wendy only after a Runtime exists
without making that lifecycle an explicit Owner setup step. Hiring cards have a
separate correctness gap: Agent creation and card completion are not atomic, so
an Agent can be created while its card remains reusable. Current clients may
also render legacy protocol fallback text beside a structured card.

## Solution

Give every configured Workspace one privileged Onboarding Agent identified only
by `workspace.onboarding_agent_id`. Workspace creation remains independent.
Before normal Workspace entry, the sole Owner connects a Computer, selects a
Runtime and Model, and explicitly creates Wendy. One setup transaction creates
or adopts Wendy, binds the platform-owned hiring skill, records the structured
Workspace binding, joins Wendy to `#general`, writes deterministic versioned
welcome messages, and completes the setup step.

Only the active bound Onboarding Agent receives recruiting instructions and may
prepare Hiring Proposals. Ordinary Agents receive neither the knowledge nor the
server authority, even if they discover the protocol. Humans retain the direct
Create Agent UI, and Owner/Admin may commit Wendy's proposals. Wendy may be
renamed because her display name never establishes authority.

Wendy uses the existing Agent archive model. Owner/Admin may edit her
configuration, but only the sole Owner may perform initial setup, archive her,
or restore her. Archiving retains the binding, disables recruiting, and creates
no replacement.

Hiring Proposal consumption and Agent creation become one transaction. A card
can succeed once, becomes durable completed audit history, updates all clients,
and no longer exposes an active Create button. Current structured-card renderers
suppress legacy fallback content such as `[agent:create proposal] <name>`.

## User Stories

1. As a Workspace creator, I want to connect a Computer before creating Wendy,
   so that Wendy always has an explicit runnable environment.
2. As a Workspace creator, I want to select Wendy's Runtime and Model, so that I
   understand the capability and cost choice before she is created.
3. As a Workspace creator, I want an explicit `Create Wendy` action, so that the
   system does not silently choose execution settings for me.
4. As a Workspace Owner, I want failed Wendy setup to be retryable without
   deleting my Workspace, so that transient setup failures do not destroy work.
5. As a Workspace member, I want normal Workspace entry gated until Wendy setup
   completes, so that every configured Workspace starts with a working
   onboarding path.
6. As a Workspace member, I want Wendy to appear in `#general`, so that the team
   knows where to ask for onboarding and team-building help.
7. As a new Workspace member, I want deterministic welcome messages from Wendy,
   so that onboarding does not depend on a successful first model turn.
8. As a Workspace member, I want later Wendy replies to come from her configured
   Runtime, so that personalized advice is genuine Agent work.
9. As a Workspace Owner or Admin, I want to edit Wendy's Runtime, Model,
   instructions, and optional skills, so that she remains useful as needs change.
10. As a Workspace Owner, I want the core hiring skill to remain platform-owned,
    so that Wendy cannot retain authority while accidentally losing the protocol.
11. As a Workspace Owner, I want to archive Wendy, so that I can intentionally
    disable Agent-assisted recruiting.
12. As a Workspace Owner, I want to restore the same Wendy, so that her original
    binding and identity return without creating a duplicate.
13. As a Workspace Admin, I want to manage Wendy's configuration but not delete
    her, so that operational delegation does not include lifecycle authority.
14. As a Workspace Member, I want Wendy lifecycle controls hidden and rejected,
    so that membership does not imply privileged team administration.
15. As an ordinary Agent, I want only work-relevant skills injected, so that my
    prompt is smaller and I am not distracted by recruiting instructions.
16. As a Workspace Owner, I want ordinary Agents denied at the hiring API even
    if they guess it, so that prompt omission is not mistaken for authorization.
17. As a renamed Wendy, I want my onboarding authority preserved, so that
    display-name customization does not break the Workspace.
18. As an ordinary Agent named Wendy, I want no onboarding authority, so that a
    name collision cannot escalate privileges.
19. As a Research Fleet lead, I want research-member management to remain a
    separate scoped capability, so that this change does not break research runs.
20. As a Human Owner or Admin, I want to create Agents directly, so that Wendy is
    an assisted workflow rather than a mandatory human bottleneck.
21. As a Human Member, I want to discuss staffing needs with Wendy, so that I can
    contribute context without receiving creation authority.
22. As a Human Member in a group, I want Wendy to place a Hiring Proposal in the
    originating conversation, so that the proposal keeps its discussion context.
23. As a channel Agent, I may see a shared Hiring Proposal, so that shared channel
    history remains canonical even though visibility grants no hiring authority.
24. As an ordinary Agent, I want requests to Wendy prevented from becoming an
    indirect recruiting path, so that Wendy cannot be used as a confused deputy.
25. As an Owner or Admin, I want to review and edit a Hiring Proposal before
    committing it, so that the human remains responsible for the final Agent.
26. As an Owner or Admin, I want one click to create exactly one Agent, so that
    retries and concurrent submissions cannot duplicate a hire.
27. As a reviewer, I want a completed Hiring Proposal to record its human actor,
    created Agent, and completion time, so that hiring remains auditable.
28. As a channel participant, I want a completed card to link to the created
    Agent and lose its Create button, so that its terminal state is obvious.
29. As a user with multiple clients open, I want card completion to update every
    client, so that a stale tab cannot invite a duplicate submission.
30. As a user on a current client, I want structured cards rendered without
    protocol fallback text, so that internal transport syntax never leaks into chat.
31. As a user on an older client, I want compatible canonical content retained
    where necessary, so that a server rollout does not create blank messages.
32. As an existing Workspace Owner with one legacy Wendy, I want the system to
    bind her without changing Runtime or Model, so that migration is non-disruptive.
33. As an existing Workspace Owner without Wendy, I want to complete the explicit
    Wendy setup, so that the system does not guess my Runtime or Model.
34. As an operator facing multiple legacy Wendy candidates, I want migration to
    stop and report the conflict, so that no Agent is silently merged or promoted.
35. As a Workspace member, I want all post-migration authority checks to use the
    structured binding, so that future renames cannot change behavior.
36. As a Workspace Owner, I want the Workspace to have exactly one Owner, so that
    Wendy lifecycle authority is unambiguous.

## Implementation Decisions

- Workspace and Wendy creation are separate lifecycle boundaries. The Workspace
  is durable before mandatory Computer and Wendy setup begins.
- A configured Workspace has exactly one Onboarding Agent binding. The binding
  is nullable during setup, same-Workspace constrained, and name-independent.
- The Workspace has exactly one Owner. General member-role editing cannot create
  a second Owner. Owner transfer UI is not added by this work.
- Initial Wendy setup is Owner-only. It requires an online Runtime plus an
  explicit Model selection and has no implicit fallback selection.
- Wendy setup atomically creates or adopts the Agent, writes the binding, binds
  the platform hiring skill, creates `#general` membership, inserts versioned
  welcome messages, and records completion. The operation is idempotent.
- Welcome messages are deterministic server-owned templates. Personalized work
  starts only after setup and is performed by Wendy's real Runtime.
- Wendy is Workspace-visible and may be renamed. UI displays an authoritative
  Onboarding Agent badge derived from the binding rather than the name.
- Owner/Admin may edit Wendy's configuration. Only Owner may archive or restore
  her. Archive preserves the binding and immediately makes the Agent ineligible
  to prepare proposals.
- The first version has no flow to designate an arbitrary replacement Agent.
- The platform hiring skill and recruiting persona are conditionally included
  only for the active bound Onboarding Agent. Other built-in work skills retain
  their current availability.
- The Agent-facing prepare API validates the authenticated Agent against the
  active Workspace binding and returns `403` for every other Agent principal.
- Human direct Agent creation remains available to Owner/Admin.
- Human Members may initiate staffing discussion. Only Owner/Admin may commit a
  Hiring Proposal. Agent-originated requests cannot cause Wendy to prepare one.
- Hiring cards may live in shared conversations. Shared visibility does not
  change skill injection or authorization.
- Hiring Proposal commit locks and consumes the prepared card in the same
  transaction that creates the Agent. It records the committing human and
  created Agent. Repeated or concurrent consumption returns a terminal conflict.
- Completed and dismissed cards are immutable terminal records. Card state is
  propagated through the existing canonical realtime/query invalidation model.
- Current renderers prefer structured action-card Parts and suppress recognized
  legacy fallback content. Compatibility content is not blindly removed from
  the server contract while old installed clients may exist.
- Migration first performs read-only counts. It preserves existing bindings,
  binds exactly one eligible legacy Wendy, requires Owner setup when none exists,
  and reports multiple candidates without choosing, merging, or deleting.
- Research Fleet creation remains outside this Workspace hiring boundary.

## Testing Decisions

Tests assert external behavior and persisted state rather than private helper
calls. The feature uses three high-level seams:

1. **Backend HTTP plus real database integration.** Extend the existing Workspace,
   Wendy, Agent action-card, and access integration suites. Exercise complete
   setup requests, binding state, role denials, archive/restore behavior,
   migration cases, transaction rollback, idempotency, and concurrent card
   commits. This is the authoritative security and exactly-once seam.
2. **Agent execution-profile contract.** Construct real claim payloads for the
   bound Wendy and an ordinary Agent in the same Workspace. Assert that Wendy
   receives the hiring skill and instructions while the ordinary Agent receives
   no hiring name, description, content, command, or API guidance. Include a
   renamed Wendy and an ordinary Agent named Wendy as negative controls.
3. **Frontend flow.** Extend existing onboarding and structured action-card
   component tests for the Setup state matrix, Runtime/Model selection,
   completed/dismissed cards, created-Agent links, fallback suppression, and
   query invalidation. Keep one Playwright path from Workspace creation through
   `Create Wendy` and welcome messages as durable user-level proof.

The original reported fallback string, `[agent:create proposal] 全栈开发`, is a
required regression fixture. A completed-card fixture must prove the Create
button is absent, and a concurrency test must prove the database contains one
Agent and one terminal card after simultaneous commits.

## Out of Scope

- Autonomous Agent hiring.
- Allowing every Agent to prepare Workspace Hiring Proposals.
- Hiding a shared Hiring Proposal from Agents who are members of that channel.
- Removing the direct human Create Agent UI.
- Assigning an arbitrary existing Agent as a replacement Onboarding Agent.
- Automatically creating a replacement after Wendy is archived.
- Adding an Owner-transfer user experience.
- Changing Research Fleet member creation.
- Implementing unrelated Agent, channel, or onboarding cleanup.

## Further Notes

- Canonical domain terms are **Onboarding Agent** and **Hiring Proposal**.
  "Wendy" is the default display name, not a role or authorization predicate.
- The current Create Agent path can create the Agent before marking its card
  done and can swallow a card-update error. Implementation must remove that
  split outcome rather than merely disabling the frontend button.
- The current task claim path appends platform built-in skills to all Agents.
  Filtering only the UI or Workspace skill bindings is insufficient.
- Current Workspace membership permits multiple Owners. The exactly-one Owner
  invariant is a prerequisite or same-delivery contract because Wendy lifecycle
  authority otherwise remains ambiguous.
- ADR-0012 records why the structured privileged binding is chosen over Raft's
  broader any-Agent recruiting model.
