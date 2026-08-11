# Role-based channel member management

Status: design review; no implementation or schema change is authorized
Date: 2026-07-29
Canonical task: #844
Reviewers: Parker, Vera

## 1. Decision

Channel member-management authorization is based on roles, not on whether the
principal or target is a human or an agent.

Human and agent requests keep separate authentication and transport boundaries:

- humans use `/api/channels/*` with a user principal;
- agents use `/api/agent/channels/*` with an `AgentPrincipal`;
- both transports adapt into the same principal-neutral decision function;
- every capability projection and write gate calls that same function.

This design does not allow an agent to call a human route or borrow its owner's
identity. It also does not widen private-channel content reads. Workspace
administrators may perform the member-management actions granted below without
being a channel member, but message, thread, attachment, project, and ordinary
channel-member read surfaces remain protected by their existing membership
rules.

The delivery is a clean cutover. It must not preserve a second
`managed_role='group_manager'` authorization path, user-shaped actor fallback,
or frontend-inferred permission matrix.

Implementation remains on hold until Parker and Vera approve this design.

## 2. Scope and non-goals

### In scope

- add one or many ordinary channel members;
- remove an ordinary channel member;
- self-leave;
- human and agent parity for channel `manager` and workspace `admin`;
- an agent workspace role of `member | admin`;
- server-computed capabilities for the member-management UI;
- actor-neutral audit and onboarding provenance;
- removal of `agent.managed_role='group_manager'` as a special authorization or
  identity path;
- race-safe enforcement and negative contract tests.

### Existing behavior retained, not expanded

- a channel owner is always a human;
- only the channel owner appoints or removes channel managers;
- only the channel owner transfers channel ownership;
- an agent can be a channel manager but never a channel owner;
- the sole-human-owner invariant remains enforced in the database;
- agent removal atomically revokes active delivery, lease, and membership state.

### Non-goals

- granting workspace administrators access to private channel content;
- changing channel archive/delete/project permissions;
- redesigning the group-manager persona, patrol cadence, or Reminder lifecycle;
- adding compatibility aliases between human and agent routes;
- preserving the temporary frontend grey-row logic from task #832;
- changing member ordering, which is owned by the separate ordering task.

## 3. Current facts and preserved ordinary-member invites

### 3.1 Current authorization

The current human single-add and batch-add handlers require only that the caller
is a current channel member. Any human channel member can therefore invite a
normal workspace user or visible agent.

This is not merely an accidental missing check:

- commit `308ff259f` is named `fix(channel): guard member invites`;
- it intentionally kept `requireChannelUserMember` as the caller gate;
- it added
  `TestAddChannelMembersRejectsPrivateAgentForPlainMember`, which exercises a
  plain member and rejects only the private-agent target;
- commit `6b7f1aa54` later introduced `channel_member.role` and explicitly
  deferred permission mutations without changing invite authority.

No product document was found that states the broader rationale, but the
targeted test is enough evidence that plain-member invitation was supported
behavior at that time.

The current frontend does not expose that API authority. Its only Add people
entry is gated by `canArchive`, which currently means channel creator or
workspace owner/admin. The new UI is therefore an intentional visibility
expansion for ordinary channel members, not merely preservation of an existing
button.

### 3.2 Product decision

The new contract preserves that existing ability and separates reversible add
from destructive remove:

- any current channel member, human or agent, may add ordinary members;
- the existing private-agent visibility restriction remains unchanged;
- an ordinary member cannot remove anyone else;
- an ordinary member may leave by removing itself;
- a workspace owner/admin or channel owner/manager may remove ordinary members;
- a channel manager cannot remove an owner or another manager.

This asymmetry is deliberate. `can_add_members` and `can_remove_members` are
independent capabilities and must never be collapsed into one
`can_manage_members` switch.

`can_add_members` is exactly:

```text
current channel member OR workspace owner/admin
```

`channel.created_by` is not a separate grant. The creator can transfer
ownership and later leave, leaving `created_by` as historical provenance with
no current membership. The existing add API already requires membership and
has no creator exception.

The workspace owner/admin half is a separate, intentional backend expansion.
Today the add API first requires channel membership. The new fallback allows a
workspace owner/admin to add or remove ordinary members in any live,
unarchived, same-workspace group without first joining it. This is the
administrative recovery path when a group no longer has an available local
manager; it is not a content-read grant.

## 4. Role model

### 4.1 Workspace roles

Humans continue to use `member.role = owner | admin | member`.

Agents gain:

```text
agent.workspace_role = admin | member
```

Rules:

- the column is `NOT NULL DEFAULT 'member'`;
- the database check excludes `owner`;
- only a human workspace owner may change an agent workspace role;
- neither a human workspace admin nor any agent, including an agent admin, can
  change agent workspace roles;
- the generic agent profile update endpoint never accepts `workspace_role`;
- no `/api/agent/*` route exists for workspace-role mutation.

Proposed owner-only human endpoint:

```http
PATCH /api/workspaces/{workspaceId}/agents/{agentId}/role
Content-Type: application/json

{"role":"member"|"admin"}
```

The handler must authenticate a human, lock and re-read the actor's workspace
membership as `owner`, lock the target agent in the same workspace, update only
`workspace_role`, write an audit event in the same transaction, then publish
after commit.

The most important negative test uses an `AgentPrincipal` with
`workspace_role='admin'` attempting to change its own role. It must fail before
any mutation or audit-success event.

### 4.2 Channel roles

`channel_member.role = owner | manager | member` remains the channel source of
truth. The existing invariants remain:

- owner rows must be human;
- an ordinary group always has at least one eligible human owner;
- managers may be human or agent;
- member authority is identical for human and agent rows.

### 4.3 Effective principal

Both transports resolve this internal value:

```go
type MemberManagementPrincipal struct {
    Kind          PrincipalKind // user | agent
    ID            UUID
    WorkspaceID   UUID
    WorkspaceRole WorkspaceRole // owner | admin | member
    ChannelRole   ChannelRole   // owner | manager | member | none
}
```

For an agent, `WorkspaceRole` comes only from `agent.workspace_role`. It never
comes from the agent owner's human membership or from `managed_role`.

`ChannelRole=none` is valid only for a workspace owner/admin operating the
member-management surface. It grants no read access to channel content.

## 5. Authorization matrix

The following matrix applies equally to human and agent principals except for
the two structural invariants marked in the table.

| Actor | Add ordinary | Remove ordinary other | Self-leave | Appoint/demote manager | Transfer owner |
|---|---:|---:|---:|---:|---:|
| Workspace owner, human only | yes | yes | if a member and owner invariant survives | no workspace override | no workspace override |
| Workspace admin, human or agent | yes | yes | if a member | no workspace override | no |
| Channel owner, human only | yes | yes | only if owner invariant survives | yes | yes, to human only |
| Channel manager, human or agent | yes | yes | yes | no | no |
| Channel member, human or agent | yes | no | yes | no | no |
| No channel membership, ordinary workspace member/agent | no | no | no | no | no |

Target rules are independent of target kind:

- "ordinary" means the target's current channel role is `member`;
- add authority comes from current channel membership even when the actor's
  channel role is `member`;
- a historical `channel.created_by` match grants nothing without current
  membership or workspace owner/admin authority;
- a manager cannot remove an owner or manager;
- a workspace admin's add/remove override does not imply channel-role mutation;
- a channel owner continues to use the existing role and ownership endpoints;
- removing oneself is `leave`, not `remove_member`, and cannot be reused to
  remove a different row;
- no role may elevate itself through add/remove.

The decision function returns a structured reason, not only a boolean:

```go
type MemberManagementDecision struct {
    Allowed bool
    Code    string
}
```

Stable denial codes include:

- `channel_not_visible`
- `channel_not_writable`
- `member_management_forbidden`
- `target_not_ordinary`
- `owner_invariant`
- `workspace_role_change_owner_only`
- `agent_cannot_be_workspace_owner`

HTTP adapters map invisible cross-workspace/private targets to `404`, valid but
unauthorized actions to `403`, malformed roles to `400`, and invariant/race
conflicts to `409`.

## 6. Private-channel boundary

Member management and content reading are separate capabilities.

A workspace owner/admin may use the management projection and add/remove
ordinary members for a live, unarchived group in the same workspace even when
the actor is not a channel member, including for a private group.

The non-member-admin management projection has an explicit response whitelist:

- channel: `channel_id`, `name`, `kind`, `archived`;
- actor capabilities: `can_add_members`, `can_remove_members`, `can_leave`;
- member row: `member_type`, `member_id`, `display_name`, `avatar_url`, `role`;
- target actions: `can_remove`,
  `remove_effect: none | clears_automation_binding`,
  `can_promote_to_manager`, `can_demote_to_member`,
  `can_transfer_ownership`.

It must not return descriptions, project bindings, runtime/session state,
activity, unread state, content previews, message-derived timestamps, or token
statistics. A normal channel member may continue using the existing richer
member list; the admin fallback receives only this management whitelist.

It must not unlock:

- channel messages or search;
- threads or attachments;
- channel project files or issue context;
- read cursors, activity, or notification state;
- agent prompt context.

The frontend must never implement this boundary by opening the normal channel
page and hiding its content components. Hidden content is already fetched
content. A future non-member-admin UI must be a dedicated management
surface/route that calls only the whitelisted management projection and write
endpoints; it must not request or mount the message stream, composer,
attachments, threads, or project/content loaders.

All existing content routes continue to require direct channel membership.
An ordinary workspace member or agent that is not a channel member receives
`404` for a private target so the channel's existence is not leaked.

## 7. Capability projection

Frontend code must not combine workspace role, channel role, member type, or
display labels to infer actions.

Dedicated endpoints avoid breaking the existing bare member-list response:

```http
GET /api/channels/{channelId}/member-management-capabilities
GET /api/agent/channels/{channelId}/member-management-capabilities
```

Both return the same wire shape:

```json
{
  "can_add_members": true,
  "can_remove_members": true,
  "can_leave": false,
  "targets": [
    {
      "member_type": "agent",
      "member_id": "uuid",
      "role": "member",
      "can_remove": true,
      "remove_effect": "clears_automation_binding",
      "can_promote_to_manager": false,
      "can_demote_to_member": false,
      "can_transfer_ownership": false
    }
  ]
}
```

Properties:

- the same decision function computes this projection and write authorization;
- the human capability handler must call `rejectAgentOnHumanRoute` before
  extracting a user principal;
- target-dependent actions are returned per row so the frontend never
  reimplements "ordinary target" logic;
- `can_add_members` and `can_remove_members` are independent actor-level
  capabilities; ordinary channel members receive `true` and `false`
  respectively;
- `can_leave` is actor-row-specific and includes the owner invariant;
- per-target `can_remove` applies the target's locked role on top of
  `can_remove_members`;
- `remove_effect` is server-computed management metadata, not automation
  configuration and not a role or badge inference. `clears_automation_binding`
  means that removing this target will also stop the group's bound
  persona/patrol automation; `none` means it will not;
- role/ownership actions reflect the retained owner-only contract;
- the projection is advisory for rendering only; every write independently
  re-resolves current state and authorizes again;
- the human and agent endpoints differ only in principal extraction.

The frontend hides actions whose capability is false. A stale client calling a
forbidden write still receives the corresponding `403` or `409`.

## 8. Write surfaces

Human routes remain:

```http
POST   /api/channels/{id}/members
POST   /api/channels/{id}/members/batch
DELETE /api/channels/{id}/members/{type}/{id}
       ?expected_remove_effect=none|clears_automation_binding
```

Dedicated agent routes are added:

```http
POST   /api/agent/channels/{id}/members
POST   /api/agent/channels/{id}/members/batch
DELETE /api/agent/channels/{id}/members/{type}/{id}
       ?expected_remove_effect=none|clears_automation_binding
```

There is no agent alias to a human handler. The two adapters may share a
principal-neutral service after authentication.

The add service validates, in order:

1. actor workspace membership/agent row and workspace role;
2. same-workspace group target and visibility/error shaping;
3. writable, non-system group;
4. add decision;
5. target existence and visibility;
6. duplicate membership as an idempotent no-op;
7. actor-neutral provenance and onboarding creation in one transaction.

The batch endpoint uses the same target validator and decision for every item.
It must not partially apply a request whose valid target later fails
authorization.

The remove service starts a transaction and locks:

1. the channel row;
2. the actor's relevant workspace/channel role rows;
3. the target membership row.

It then re-runs the decision against the locked target role. A manager racing
with a target promotion must never remove the newly promoted manager.

Under the channel lock the service recomputes `remove_effect` as
`clears_automation_binding` when `group_manager_agent_id` equals the agent
target, otherwise `none`. The request must carry the exact effect shown to and
confirmed by the user. If `expected_remove_effect` is missing, malformed, or
differs in either direction from the locked effect, the service returns
`409 remove_effect_changed` and changes nothing. The frontend refreshes and
re-confirms with the truthful copy; it never retries silently.

For a matching `clears_automation_binding` request, the service clears
`group_manager_agent_id` and removes the membership in the same transaction.
The audit row records both the previous bound agent ID and
`group_manager_binding_cleared=true`; the mutation response returns the same
effect so the client can report the truthful outcome.

Agent target removal calls the existing revoke primitive inside the same
transaction so membership, active delivery, lease, and session cleanup remain
atomic. Human target removal retains the sole-owner database guard. System/audit
rows are inserted before commit; realtime publication happens only after
commit.

## 9. Actor-neutral provenance

This is an independently valid integrity fix. Its deployment must not depend on
whether agent workspace admins ship in the same release.

Current defects:

- `channel_member.added_by` references only `user`;
- `channel_agent_onboarding.source_actor_id` references only `user`;
- channel member system-event helpers hardcode actor type `user`.

The migration replaces the user-shaped columns rather than adding permanent
compatibility fields:

```text
channel_member.added_by_type  user | agent | system
channel_member.added_by_id    UUID nullable only for system

channel_agent_onboarding.source_actor_type  user | agent | system
channel_agent_onboarding.source_actor_id    UUID nullable only for system
```

There is intentionally no polymorphic foreign key. Transactional validators and
write services enforce same-workspace actor existence; CHECK constraints enforce
the allowed shapes. Existing non-null user IDs backfill as `user`; historical
null values backfill as `system`.

All trigger-generated membership/onboarding rows propagate both actor fields.
The migration must preserve migration 209's exclusion for `env_dispatch`
inserts.

The onboarding session creator remains a human-only legacy field. It never uses
historical `channel.created_by`, because that user may have transferred
ownership, left the channel, or left the workspace. When onboarding is
materialized:

- audit and prompt provenance use the real agent actor;
- the required session creator is resolved from the channel's current,
  same-workspace human `owner` membership as a mechanical fallback only;
- that fallback must never be exposed as the action actor.

If the current unique human owner cannot be resolved, materialization fails
closed with an owner-invariant error before creating a chat session, claiming
the onboarding, or emitting a success audit/event. It must not substitute
`system`, the historical creator, or an out-of-workspace user ID.

The down migration can restore user provenance only. Agent provenance becomes
null/system on rollback; deployment notes must call out that rollback is lossy.

## 10. Absorbing `managed_role='group_manager'`

`agent.managed_role='group_manager'` must not survive as a second role system.
It is replaced by two orthogonal facts:

- authorization: `channel_member.role='manager'`;
- optional group-manager automation binding:
  `channel.group_manager_agent_id=<ordinary agent id>`.

The binding may identify which ordinary agent runs the group's persona/patrol,
but it grants no permission. Every permission comes from workspace and channel
roles.

Current production callers are cut over as follows:

| Current caller | Replacement |
|---|---|
| Beckham provisioning writes `managed_role` | create an ordinary agent, bind `channel.group_manager_agent_id`, insert its membership as `role='manager'` |
| `groupManagerAgentIDs` and general-directory exclusion | delete the exclusion; visibility and ownership govern ordinary agent discovery |
| manual invite rejection for managed agents | delete the special rejection; normal target visibility and membership rules apply |
| workspace-member special profile access/update | delete the exception; ordinary agent ownership/admin rules apply |
| agent response/inbox `managed_role` fields | remove from API and daemon contracts |
| radar assignment rejects any managed role | delete role-based rejection; assignment follows normal availability/membership rules |
| Wendy diagnostic prompt prints `managed_role` | remove the field |
| patrol bootstrap validates `managed_role` | validate live same-workspace bound agent plus `channel_member.role='manager'` |
| migration/backfill predicates on `managed_role` | join from `channel.group_manager_agent_id` and manager membership |

After every production caller and generated model is cut over, the same
migration drops:

- `agent.managed_role`;
- its CHECK constraint and partial index.

`channel.group_manager_agent_id`, Reminder `origin_kind`, and the group-manager
persona are not permission sources or a separate automation mechanism. The
Channel Manager Role is a persistent responsibility: assignment tells the Agent
to manage ordinary self-owned Reminders, and removal durably tells the Agent to
cancel those it no longer needs. The service does not create, classify, or
cancel a role-specific Reminder subtype.

No dual-read, dual-write, or fallback to `managed_role` is permitted.

## 11. Audit and system events

Every successful mutation records:

- actor type and actor ID;
- actor workspace role and channel role at decision time;
- target type, target ID, and locked target role;
- action (`member_added`, `member_removed`, `member_left`,
  `agent_workspace_role_changed`);
- channel/workspace IDs;
- request/correlation ID.

When workspace owner/admin authority is used without channel membership, the
audit row also records:

```text
authority_source = workspace_admin_override
```

This is required on both add and remove. It distinguishes recovery use from
ordinary in-channel management and must be queryable without parsing prose.

System events use the true actor type. An agent admin action must appear as an
agent action, never as its owner user. Failed authorization creates diagnostic
logs but no success event.

Database mutation and durable audit/system-event insertion commit atomically.
Realtime publication occurs only after the commit succeeds.

## 12. RED contract matrix

The design review should approve these tests before implementation.

### Shared decision tests

- human manager and agent manager receive identical decisions;
- human workspace admin and agent workspace admin receive identical add/remove
  decisions;
- ordinary human member and ordinary agent member can still add ordinary
  members;
- ordinary human member and ordinary agent member cannot remove another member;
- a mutation of the capability implementation that collapses add/remove into a
  manager-only switch makes both ordinary-member add regressions fail;
- manager can add/remove an ordinary human and ordinary agent;
- manager cannot remove a human manager, agent manager, or owner;
- workspace admin can manage a same-workspace private group without content-read
  capability;
- non-member workspace admin can add an ordinary member to an unarchived
  private group and records `authority_source=workspace_admin_override`;
- non-member workspace admin can remove an ordinary member from an unarchived
  private group and records `authority_source=workspace_admin_override`;
- the same non-member workspace admin is denied message/thread/attachment reads
  for that private group;
- non-member workspace admin cannot mutate an archived group;
- ordinary member, channel manager, channel owner, and workspace owner/admin
  are all denied add/remove mutations on an archived group;
- non-member ordinary principal receives fail-closed denial;
- target kind never changes a decision when roles are equal.

### Bound automation removal tests

- capability projection marks only the currently bound agent target with
  `remove_effect=clears_automation_binding`; every other target receives
  `remove_effect=none`;
- a missing/malformed `expected_remove_effect` returns `400`; either mismatch
  against locked state (`none -> clears_automation_binding` or the reverse)
  returns `409 remove_effect_changed`, with zero membership, binding,
  success-audit, or success-event mutation;
- a confirmed remove atomically clears `group_manager_agent_id`, removes the
  membership, revokes the agent delivery/session state, and records
  `group_manager_binding_cleared=true` plus the previous bound agent ID;
- a bind/rebind racing with removal is decided against the locked channel row,
  so the server never clears or removes a different newly bound agent;
- removing an unbound ordinary member never reports or audits a binding clear.

### Onboarding creator tests

- agent-authored onboarding records the real agent provenance while the legacy
  chat-session creator is the current same-workspace human channel owner;
- ownership transfer followed by the historical creator leaving the
  channel/workspace still resolves the new current owner;
- missing, duplicate, non-human, or cross-workspace owner state fails closed
  before session/onboarding claim or success event and never falls back to
  `channel.created_by`.

### Transport tests

- AgentPrincipal is rejected on every human member-management route;
- agent routes reject user/session authentication;
- human and agent capability payloads are identical for equivalent roles;
- hidden UI action and write denial derive from the same decision code.

### Workspace role tests

- only a human workspace owner changes an agent `member <-> admin`;
- human admin cannot change an agent workspace role;
- agent admin cannot change another agent workspace role;
- agent admin attempting to change its own workspace role is denied and leaves
  the row/audit stream unchanged;
- `owner` for an agent is rejected by handler and database.

### Transaction and race tests

- target promotion racing manager removal yields no illegal removal;
- owner invariant survives concurrent leave/remove;
- removing an agent atomically revokes delivery/lease/session state;
- audit insertion failure rolls back membership mutation;
- realtime event is absent before commit and after rollback;
- batch add is all-or-nothing for authorization failure.

### Provenance tests

- human add records `user/<human-id>`;
- agent add records `agent/<agent-id>`;
- system insert records `system/null`;
- onboarding prompt/audit keeps the agent actor while the mechanical session
  creator remains human;
- env-dispatch rows retain migration 209 behavior;
- no path writes an owner-user ID as an agent action actor.

### Beckham cutover tests

- bound group-manager agent is an ordinary agent with channel role `manager`;
- unbound ordinary manager has the same member-management authority;
- binding without manager membership grants no member-management authority and
  prevents patrol bootstrap;
- no SQL, API, daemon, or generated model references `managed_role`;
- no special directory/profile/assignment permission survives.

## 13. Migration and rollout

The exact migration number is chosen at implementation time from current
`origin/dev`; the present tail is 243 with no 242 file.

Recommended phases:

1. **Contract gate** — approve this document and the ordinary-member decision.
2. **Independent provenance migration** — actor-neutral columns, backfill,
   trigger replacement, tests, and lossy-down deployment note.
3. **Role migration** — `agent.workspace_role`, owner-only mutation endpoint,
   audit, and self-escalation negatives.
4. **Beckham clean cutover** — first publish a daemon version whose contracts
   and behavior no longer consume `ManagedRole`, and require that version as the
   fleet floor. Then replace every server `managed_role` caller, backfill bound
   agents to channel manager, remove the API/daemon field, regenerate models,
   and drop the column in one server migration. No server dual-read or
   `managed_role` fallback is allowed. The column-drop deploy is blocked until
   the daemon floor is observed across every eligible non-archived runtime.
5. **Authorization core** — shared decision function and negative matrix.
6. **Human and agent writes** — separate adapters, shared service, race gates.
7. **Capability projection and frontend cutover** — task #845 consumes only
   server capabilities and removes temporary grey rows.
8. **Release proof** — focused tests, full backend/frontend gates, migration
   apply on a production-shaped copy, deploy, served SHA, and live human/agent
   parity checks.

Deployment notes must state:

- migrations to apply and their order;
- actor-provenance rollback loss;
- preservation of historical ordinary-member invites and the independent
  destructive-remove restriction;
- removal of `managed_role` from API/runtime payloads;
- no private-content read expansion;
- exact live probes for human manager, agent manager, and agent admin.

## 14. Frontend contract for task #845

The frontend:

- renders `owner | manager | member` from the backend member row;
- renders only server-provided action capabilities;
- never checks `member_type` to decide authority;
- never maps `managed_role`, `manager_agent`, or `manager_human`;
- hides unavailable menu items rather than showing disabled speculative actions;
- intentionally exposes Add people to ordinary human and agent channel members
  for the first time when `can_add_members=true`;
- reads Add people visibility/action only from `can_add_members`, never
  `can_remove_members`;
- introduces a dedicated member-add gate and does not change `canArchive`,
  whose archive/project-edit consumers remain separate;
- reads `remove_effect` only from the backend target action. It never derives
  this effect from the manager badge, member type, or local channel state;
- when that effect is `clears_automation_binding`, requires a destructive
  confirmation whose body is
  `该成员是本群当前的自动化绑定。移出后会同时解除该绑定，与该成员关联的巡检和角色自动化将停止。你可以稍后重新绑定自动化成员。`,
  whose primary action is `移出并解除绑定`, and whose success message is
  `已移出成员并解除自动化绑定`; ordinary removal does not show this copy;
- submits the exact `expected_remove_effect` the user confirmed. A stale
  projection that receives `remove_effect_changed` must refresh and ask again
  with the correct ordinary or automation-clear confirmation; it must not
  silently retry or fall through to the generic remove-failure presentation.
  The typed code is consumed at the target-row removal flow, using the existing
  typed-error-to-local-state pattern rather than a new global error surface.
  The transition message is
  `成员状态已更新。请查看最新操作影响后再次确认。`; after refresh, the user must
  explicitly click the current confirmation action again;
- refreshes both member rows and capabilities after a mutation or `403/409`;
- deletes task #832's temporary grey rows when the real write paths ship.

Badge text and ordering remain separate presentation concerns and must not become
authorization inputs.

Frontend acceptance reports the ordinary-member Add people row as an explicit
UI expansion. API contract tests own the historical behavior regression; the
frontend separately proves that ordinary human and agent members can now see
and complete the add flow, still cannot remove others, and remain subject to
the private-agent invitation restriction.

Task #845 covers admins that are already channel members; their content access
comes from membership, not from the admin role. A non-member admin management
surface is explicitly deferred to a follow-up delivery. Until it exists, release
reporting must say: backend recovery capability is available, while the
non-member private-channel admin UI entry remains pending. Task #845 must not be
reported as complete frontend coverage of the admin fallback.

## 15. Review checklist

Parker and Vera should explicitly confirm:

- [ ] ordinary-member invite preservation and add/remove capability split are
      explicit;
- [ ] same role means same permissions for human and agent;
- [ ] workspace admin management does not bypass private content reads;
- [ ] channel owner remains human-only and role/transfer remains owner-only;
- [ ] capability projection and write gates share one decision function;
- [ ] agent workspace role is owner-mutated only;
- [ ] provenance repair is independently releasable;
- [ ] `managed_role` is fully removed, with no compatibility path;
- [ ] migration, rollback, audit, and race contracts are sufficient to begin RED
      implementation.
