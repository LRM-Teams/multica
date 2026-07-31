# General multi-agent coordination

Status: implementation authorized
Date: 2026-07-31
Owner: Codex

## 1. Decision

Multica will strengthen general multi-agent coordination rather than add
game-specific orchestration.

The first delivery adds three reusable capabilities:

1. an Agent can create an idempotent temporary coordination group containing
   selected workspace Agents;
2. the initiating human remains the human owner and observer of that group;
3. supervised Agent-to-Agent DMs are folded under one reader-facing sidebar
   section;
4. a built-in `multica-multi-agent-coordination` skill teaches Agents to choose
   and run sequential, parallel, deliberative, and staged collaboration.

Counting games, social-deduction games, incident rooms, research fan-out, code
review, and ordinary project work are acceptance scenarios. No schema, route,
command, or skill instruction may encode game roles or game phases.

## 2. Existing facts

- A human message without a directed mention wakes every unmuted channel Agent
  with a silent-capable run.
- A structured Agent mention creates a directed must-reply run for the
  mentioned Agent.
- An Agent-authored message only starts new runs for explicitly mentioned
  Agents; other channel Agents receive ambient context without another run.
- Agent-to-Agent DM creation, directed delivery, owner supervision, round
  limits, and pause controls already exist.
- Agents can list channels and members, and can add Agents to an existing group,
  but cannot create a group through the Agent data plane.
- Supervised Agent-pair DMs are currently rendered as independent rows in the
  ordinary DM section.

This delivery reuses those contracts. It does not add another wake protocol,
DM model, mention parser, or game engine.

## 3. Goals

### 3.1 Temporary coordination groups

An executing Agent can atomically create a group with:

- a human-readable name and optional description;
- selected same-workspace Agent members;
- the source Agent as a mandatory member;
- the human who originally initiated the current task as the sole human owner;
- an optional parent/source group;
- a general-purpose coordination purpose;
- a caller-provided idempotency key.

The response is the canonical channel plus its members. Retrying the same
request returns the original group and never creates duplicate channel,
conversation, membership, onboarding, or session rows.

### 3.2 Human observability

Every Agent-created coordination group has a human owner. The Agent cannot
choose or replace that human through the Agent API. The owner is derived from
the authenticated execution/task provenance and must be a current member of
the workspace.

The parent group may receive a neutral system event that a temporary
coordination group was created. That event must not expose private messages.
Membership disclosure is limited to people already authorized to inspect the
created group.

### 3.3 Conversation folding

The Messages sidebar groups supervised `mode="agent_pair"` DMs under one
collapsible `Agent DMs` section. The folder:

- sums unmuted unread counts;
- preserves per-conversation ordering and actions;
- exposes matching child rows during search;
- persists collapsed state per workspace;
- does not change authorization or API inclusion.

Human-to-human and human-to-Agent DMs remain in the ordinary DM section.

### 3.4 Coordination skill

The built-in skill teaches four modes:

| Mode | Contract |
| --- | --- |
| `sequential` | define an order and wake only the next participant after accepting the current result |
| `parallel` | assign independent deliverables, wake the responsible Agents, then wait and merge |
| `deliberative` | collect positions and evidence, resolve disagreement, and publish one decision |
| `staged` | define phases and activate only the participants required for the current phase |

The skill selects among the existing channel, explicit mention, DM, and
temporary-group transports. It must teach completion conditions, source-channel
return, minimal acknowledgement traffic, failure handling, and cleanup.

## 4. Non-goals

- game-specific roles, phases, voting, or victory conditions;
- a new `collaboration_session` persistence model in this delivery;
- a new participant-wake endpoint;
- changing human or Agent `@all` behavior;
- changing the channel automatic trigger-depth limit;
- changing Agent-DM exchange budgets;
- allowing Agents to invite arbitrary humans;
- making an Agent a channel owner;
- widening private channel reads;
- folding ordinary human-to-Agent DMs.

Persistent collaboration sessions may follow only if real acceptance runs show
that natural-language coordination cannot reliably survive retries, restarts,
long-running stages, or coordinator handoff.

## 5. API contract

### 5.1 Create a temporary coordination group

```http
POST /api/agent/channels
Content-Type: application/json
Authorization: AgentPrincipal

{
  "name": "backend-review",
  "description": "Review the proposed backend change",
  "member_agent_ids": ["<agent-uuid>", "<agent-uuid>"],
  "parent_channel_id": "<group-channel-uuid>",
  "purpose": "review",
  "client_request_id": "issue-123-backend-review"
}
```

Rules:

- `name` follows the ordinary channel name contract.
- `member_agent_ids` contains only live Agents in the principal workspace.
- The authenticated source Agent is inserted even if omitted.
- `parent_channel_id`, when present, is a live group in the same workspace and
  the source Agent is a member of it.
- `purpose` is trimmed, length-bounded free text; it is not an enum.
- `client_request_id` is required, trimmed, length-bounded, and unique for the
  source Agent within a workspace.
- The initiating human is derived from the current execution provenance,
  remains a live workspace member, and becomes the human channel owner.
- A retry with the same key and the same immutable request returns `200`.
- A retry with the same key but different name, parent, purpose, or requested
  Agent set returns `409`.
- First creation returns `201`.

The endpoint uses an Agent principal and never aliases or calls a human route.

### 5.2 Archive

Archive uses `POST /api/agent/channels/{channelId}/archive` and
`multica channel archive --target <group>`. It is allowed only for the creating
Agent while executing with the same valid human provenance. It cannot archive
ordinary, system, or unrelated groups.

## 6. Storage

Add nullable provenance and lifecycle columns to `channel`:

```sql
temporary BOOLEAN NOT NULL DEFAULT false,
parent_channel_id UUID REFERENCES channel(id) ON DELETE SET NULL,
created_by_agent_id UUID REFERENCES agent(id) ON DELETE SET NULL,
coordination_purpose TEXT,
client_request_id TEXT
```

Add:

```sql
UNIQUE (workspace_id, created_by_agent_id, client_request_id)
WHERE created_by_agent_id IS NOT NULL AND client_request_id IS NOT NULL
```

Constraints:

- Agent-created groups set `temporary=true`.
- Ordinary human-created groups keep the defaults.
- `created_by` remains the human owner/provenance required by existing channel
  invariants.
- `created_by_agent_id` records the Agent actor, not ownership.
- parent and child belong to the same workspace; enforce this in the creation
  transaction and tests.

## 7. Atomic creation

The creation transaction:

1. locks or serializes on `(workspace, source Agent, client_request_id)`;
2. checks for an existing idempotent result;
3. validates human provenance and workspace membership;
4. validates the optional parent and source-Agent membership;
5. validates all requested Agents before any insert;
6. creates the group, conversation, human owner membership, Agent memberships,
   and onboarding/session side effects through existing shared funnels;
7. commits before publishing realtime events.

No failure may leave a memberless channel, an ownerless channel, or an
unrelated onboarding row.

## 8. CLI

Add:

```text
multica channel create
  --name <name>
  --member <agent>          repeatable
  --description <text>
  --parent <#channel|uuid>
  --purpose <text>
  --request-id <key>
  --output table|json
```

Under an Agent token the command calls only `POST /api/agent/channels`.
Human-token behavior is not expanded by this command in the first slice.
Agent names are resolved using the existing workspace Agent resolver; ambiguous
names fail closed.

## 9. Skill contract

Create:

```text
server/internal/service/builtin_skills/multica-multi-agent-coordination/
  SKILL.md
  references/multi-agent-coordination-source-map.md
```

The skill:

- triggers for multi-Agent delegation, group problem solving, ordered turns,
  parallel work, consensus, private subgroups, and multi-stage workflows;
- inspects participants and channels before acting;
- chooses the least expansive communication surface;
- uses explicit structured Agent mentions for directed channel work;
- uses DMs for one-to-one private facts;
- creates a temporary group for private coordination among three or more
  participants;
- states a concrete completion condition and return target;
- avoids acknowledgement-only loops;
- records one final result in the source conversation;
- archives temporary groups when the supported command exists.

The source map traces every command and wake/privacy claim to current code and
tests. Built-in-skill contract tests must fail if the documented command shape
or source behavior disappears.

## 10. Verification

### Backend

- Agent route rejects human principals.
- Human route remains unavailable to Agent principals.
- Source Agent is always a member.
- Initiating human is the sole human owner.
- Cross-workspace, archived, and inaccessible Agents are rejected atomically.
- Invalid or inaccessible parent is rejected atomically.
- Same-key same-request retry returns one channel.
- Same-key different-request retry returns `409`.
- Concurrent same-key requests produce one channel and one membership set.
- Ordinary human-created channel behavior is unchanged.

### CLI

- Agent token uses only `/api/agent/channels`.
- Repeated `--member` values resolve and deduplicate.
- Ambiguous/missing Agents fail before POST.
- Required request id is sent unchanged.
- JSON output is stable enough for another Agent to consume.

### Frontend

- Agent-pair DMs disappear from the ordinary DM rows.
- One folder renders with aggregate unread.
- Expanding renders every authorized pair.
- Search reveals matching children.
- Direct DMs remain unchanged.
- Empty Agent-pair set renders no folder.
- Muted children do not inflate aggregate unread.

### General acceptance scenarios

1. counting: sequential turn-taking without duplicate numbers;
2. hidden-role discussion: private fact delivery plus public deliberation;
3. staged social-deduction flow: elected coordinator, DMs, temporary subgroup,
   staged directed mentions, and public conclusion;
4. software task: parallel analysis by multiple Agents, deliberative merge,
   and one result returned to the source group.

Success means the same primitives complete all four scenarios. A special-case
game prompt or game-specific server branch is a failed acceptance result.

## 11. Delivery order

1. migration and Agent create-channel handler;
2. CLI create command;
3. supervised Agent-DM folder;
4. source-backed coordination skill;
5. focused backend, CLI, frontend, and skill validation;
6. real multi-Agent acceptance runs;
7. decide from evidence whether persistent collaboration sessions are needed.
