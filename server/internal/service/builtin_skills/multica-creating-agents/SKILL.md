---
name: multica-creating-agents
description: "Use when creating, inspecting, or debugging Multica agents via Web UI / HTTP (`POST /api/agents`) — field contracts, env gating, skill binding. There is no multica agent management CLI; list with workspace info --agents."
user-invocable: false
allowed-tools: Bash(multica *)
---

# Creating Multica agents

This is the contract for Multica's agent-creation path: what create entry points
accept, what the server validates and rejects, how each field is persisted, and
which fields the daemon reads at claim time. It is not a parameter manual — it
states source-traced facts, and every claim is backed by `file:line` in
`references/creating-agents-source-map.md`.

## Surface (current)

**There is no `multica agent *` management CLI** (get/create/update/archive/skills/env
removed). Use real replacements:

| Need | Surface |
|---|---|
| List agents | `multica workspace info --agents` / `--output json` |
| Create / edit / archive | Multica **Web UI** → `POST/PUT/DELETE /api/agents` |
| Hire (agent → human) | agent-created **agent:create Proposal Message** → owner/admin opens CreateAgentDialog |
| Skill binding | Web UI agent settings → `POST/PUT /api/agents/{id}/skills…` |
| Env secrets (owner/admin) | Web UI → `GET/PUT /api/agents/{id}/env` (agent actors denied plaintext) |

Read-only list (no side effects):

```bash
multica workspace info --agents --output json
```

The active Workspace Onboarding Agent prepares a human-confirmable Hiring
Proposal in the originating conversation with:

```bash
multica action prepare \
  --type agent:create \
  --name "<permanent lowercase name>" \
  --description "<short role summary>" \
  --target "#<channel>" \
  --client-request-id "<stable retry id>"
```

The command posts to `/api/agent/actions/prepare`. It prepares a card only; an
Owner or Admin must review and commit it. The server authorizes this endpoint
from `workspace.onboarding_agent_id`, so another Agent must not proxy or ask the
Onboarding Agent to prepare a proposal on its behalf.

Only a Human-authored staffing request may become a Hiring Proposal. If the
requester is an Agent, do not call `multica action prepare`, even when that Agent
quotes or forwards Human text. Reply that a Human must initiate the staffing
discussion. Shared-channel visibility of an existing Proposal is not authority
to prepare another one.

## Core model

An agent is a workspace-scoped row (table `agent`). Creation is a single
`POST /api/agents` from the Web UI (or a human-confirmed Proposal Message that
opens the same dialog). At task claim time the daemon re-reads the agent row and
assembles the runtime payload — so the **persisted** fields, not create-time
CLI output, are what the agent runs on.

The Workspace Onboarding Agent uses the same generic Agent creation transaction.
Its additional role is identified only by `workspace.onboarding_agent_id`;
`Wendy` is a default display name, not an identity or authorization predicate.

Two distinct text fields, often confused:

- `description` is a catalog summary. It is stored and shown in listings; the
  daemon does NOT inject it into the agent's runtime prompt. Treat it as
  human-facing metadata only. Capped at 255 Unicode code points.
- `instructions` is the runtime behavior contract. The daemon reads it at
  claim time and ships it to the provider as the agent's durable instructions.
  Persona, responsibilities, boundaries, output and escalation rules go here,
  not in `description`.

## Create entry points

Human create goes through Web UI Create Agent (or agent:create Proposal Message
→ same dialog). The dialog posts to `POST /api/agents`; the server requires the
human `manageAgents` capability (workspace owner/admin) and rejects agent
principals on this route.

On current servers, `name` is the permanent Agent name used by mentions and in
persisted API responses. It is required at creation. `display_name`
is optional at creation and remains editable later; when omitted, the initial
display name is `name`.

The HTTP body (`CreateAgentRequest`) accepts: `name`, optional `display_name`,
`description`, `instructions`, `runtime_id`, `runtime_config`,
`avatar_selection`, `custom_env`, `custom_args`, `model`, `thinking_level`,
`visibility`, `max_concurrent_tasks`, `mcp_config`.

## Field contracts

| Field | Persisted as | Validated? | Consumed by |
|---|---|---|---|
| `name` | `agent.name` + initial display fallback | required at create; permanent; lowercase ASCII name unique per workspace | mention routing / bare @name fallback |
| `display_name` | `agent.display_name` | optional at create; update rejects empty | listings, runtime payload labels |
| `description` | `agent.description` | 400 if > 255 code points | catalog/listing only — NOT the runtime prompt |
| `instructions` | `agent.instructions` | none | daemon → provider at claim time |
| `avatar_selection` | server-derived `avatar_url` / `avatar_source` / optional attachment | omit create → assigned immutable OSS preset; omit update → no change; picked catalog URL/uploaded attachment verified | durable member identity |
| `runtime_id` | `agent.runtime_id` | required (400) + must resolve in workspace | selects runtime/provider |
| `model` | `agent.model` (nullable) | none beyond runtime support | daemon reads; empty = runtime default |
| `thinking_level` | `agent.thinking_level` (nullable) | provider-level enum; unknown → 400 | daemon; empty = runtime default |
| `custom_args` | JSON array | shape checked client-side; server stores as-is | daemon (extra CLI switches); defaults `[]` |
| `runtime_config` | JSON | shape checked client-side; server stores as-is | runtime-specific; defaults `{}` |
| `custom_env` | JSON object | — | daemon process env; see Env & secrets |
| `mcp_config` | raw JSON | object or `null`; create drops literal `null` | daemon MCP; redacted on read |
| `visibility` | string | — | access control; default `private` |
| `max_concurrent_tasks` | int | — | scheduler cap; default `6` |

Defaults when omitted: `display_name` from legacy `name` seed, `runtime_config`
→ `{}`, `custom_env` → `{}`, `custom_args` → `[]`, `visibility` → `private`,
`max_concurrent_tasks` → `6`; omitted `avatar_selection` gets one concrete
preset with `avatar_source=assigned`. Presets are absolute, immutable CDN URLs
backed by OSS; older `/agent-avatars/human-XX.jpg` selections are normalized to
the same visual asset in the retained v1 catalog at the write boundary. Raw
`avatar_url` is rejected.

`thinking_level` is validated only at the provider level: unrecognized literal
→ 400; a value valid for the provider but unsupported for the chosen model is
NOT rejected at create — that surfaces as a daemon task error at run time.

### model vs custom_args

`model` is a first-class persisted column the daemon reads directly.
`custom_args` are raw provider CLI args. Some providers reject `--model` inside
`custom_args` — that is client guidance, not a server-enforced invariant.

## Env & secrets

`custom_env` is secret material. Humans set it in Web UI agent settings (or
owner/admin HTTP). Never put secrets in shell history or process lists.

Read-side facts:

- Agent resources never expose plaintext `custom_env`. List/get/create/update
  and WS events return only `has_custom_env` (bool) and `custom_env_key_count`
  (int).
- Reading plaintext requires `GET /api/agents/{id}/env`. Gated to workspace
  **owner/admin**; **agent actors are denied** regardless of member role.
- Writing after create does NOT go through generic agent update. That handler
  rejects `custom_env` with 400 ("use PUT /api/agents/{id}/env"). Writes go to
  `PUT /api/agents/{id}/env` (owner/admin, audited).

### mcp_config

`mcp_config` is MCP server configuration (JSON object such as
`{"mcpServers": {…}}`). Also secret-ish (tokens). Differs from `custom_env`:

- **It IS settable through generic agent update** (`PUT /api/agents/{id}`):
  omit → no change; `null` → clear; object → replace. No dedicated env endpoint.
- **Serialized on read but redacted** for callers not allowed to view secrets;
  agent actors never see plaintext MCP config.

## Skill binding

Creating an agent does NOT bind any workspace skill — binding is a separate
operation after the agent exists (Web UI settings or human HTTP):

- **add** is additive — merges given ids with existing bindings
  (`POST /api/agents/{id}/skills/add`).
- **set** is replace-all — overwrites the entire binding list
  (`PUT /api/agents/{id}/skills`); empty list clears all. `set` is the
  replacement path.

```text
POST /api/agents/{id}/skills/add   body: { "skill_ids": ["..."] }
PUT  /api/agents/{id}/skills       body: { "skill_ids": ["..."] }
GET  /api/agents/{id}/skills       list current bindings
```

At claim time the daemon assembles workspace-bound skills first, then appends
platform built-in skills. Capability belongs in a bound skill, not pasted into
`instructions`.

## Side effects needing approval

Read-only (safe): `workspace info --agents`, Web UI agent detail, list skill
bindings.

State-changing (require explicit human instruction — do not run speculatively):

- Web UI / committed agent:create Proposal Message → inserts a new agent row.
- Skill add / set → mutate bindings (`set` is destructive).
- Env set → overwrites full `custom_env` and writes an audit row.

Agents do **not** create other agents via CLI. Hire path = Proposal Message for
a human with `manageAgents`. Research fleet hire is a separate specialty path.

## Common wrong assumptions

- "`description` is the prompt." It is not — only `instructions` reaches the
  runtime.
- "`agent.name` is the editable display label." `agent.name` is permanent;
  render and edit `display_name` when present.
- "Create binds the agent's skills." It does not; bind afterward.
- "Generic update can rotate env." It cannot — 400 on `custom_env`; use the
  env endpoint.
- "`mcp_config` behaves like `custom_env` on update." It does not — mcp is
  settable via generic update; only custom_env is gated behind `/env`.
- "Agent list/get CLI still exists." It does not — use `workspace info --agents`
  and Web UI / HTTP.
- "`set` and `add` are interchangeable for skills." `set` replaces all bindings.

## References

`references/creating-agents-source-map.md` maps contracts to `file:line` on the
current tree (HTTP/handler focus after CLI removal).
