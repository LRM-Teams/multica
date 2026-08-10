# Creating agents — source map

Evidence layer for `SKILL.md`. Every contract maps to `file:line` on the
current tree, the runtime effect, and a safe read-only check. Line numbers
drift — surrounding context is the anchor.

## Verification

```bash
go test ./internal/service -run TestCreatingAgentsSkillCoversAgentCreationContracts
go test ./internal/service -run TestBuiltinSkillsConformToTemplate
```

## CLI surface (post cut)

There is **no** `multica agent *` management command group. Listing:

| Contract | Source | Behavior |
|---|---|---|
| List agents only | `server/cmd/multica/cmd_workspace.go` (`--agents` flag on `workspace info`) | `multica workspace info --agents` / `--output json` |
| Help example | `server/cmd/multica/help.go` | Documents `workspace info --agents` |

Create / update / archive / skills / env management: **Web UI + HTTP** only.

## HTTP create / update — `server/internal/handler/agent.go`

| Contract | Line (approx) | Behavior |
|---|---|---|
| `maxAgentDescriptionLength = 255` | 31 | Cap is 255 **Unicode code points** (`utf8.RuneCountInString`) |
| `AgentResponse` no plaintext `custom_env` | 36–147 | Only `has_custom_env`, `custom_env_key_count`; avatar truth persisted |
| `CreateAgentRequest` fields | `agent.go:CreateAgentRequest` | name/optional display_name, description, instructions, runtime_id, model, thinking_level, custom_env, mcp_config, … |
| permanent name required | `agent.go:CreateAgent` | lowercase ASCII `name` is required; display_name cannot replace it |
| `description` ≤ 255 | ~660–662 | excess → 400 |
| `runtime_id` required + workspace resolve | ~664–679 | missing/unknown → 400 |
| `thinking_level` provider validation | ~702–708 | unknown literal → 400; model-specific gaps deferred to daemon |
| Defaults `{}` / `[]` / private / 6 | ~668–734 | before insert |
| `mcp_config` null-skip on create; redacted on read | ~56, 573–739 | |
| Avatar verification | `agent.go` + `agent_avatar.go` | omit → assigned; picked/uploaded verified; raw URL rejected |
| `UpdateAgent` rejects `custom_env` | ~929–938 / 1476 | 400 → use `PUT /api/agents/{id}/env` |
| `UpdateAgent` rejects `name` / `username` | `agent.go:UpdateAgent` | permanent name can only be set during creation; edit display_name instead |

## LRM-2343 Proposal and first-start lifecycle

| Contract | Source | Behavior |
|---|---|---|
| Human create authorization | `handler/agent.go`, `handler/agent_template.go`, `handler.go:requireManageAgents` | owner/admin only; AgentPrincipal on the human route → 403 |
| Canonical Proposal | `handler/agent_action.go`, `channel_message.parts`, `agent_action` | `agent:create` is one visible Message plus an atomically seeded commit record; no action-card row or dismiss state |
| Onboarding prepare command | `cmd/multica/cmd_action.go`, `handler/agent_action.go`, `workspace.onboarding_agent_id` | `multica action prepare` calls `/api/agent/actions/prepare`; only the active bound Onboarding Agent is authorized |
| Commit idempotency | `handler/agent_action_commit.go` | action Message ID + non-secret final-payload hash returns the same Agent on a safe replay and rejects divergent payloads |
| Atomic provisioning | `createAgentManagedCommit`, template create transaction | Agent identity, system `#general` membership/onboarding, and one `agent_start_intent` commit together |
| Durable first start | migrations 288–289, `handler/agent_start_intent.go`, `daemon/agent_start_intent.go` | stable dispatch ID retries until Computer acceptance; `ready`/`failed` are later sequence-guarded observations, and failed work is not auto-restarted |
| Human read model | `AgentResponse.StartIntentStatus`, `EventAgentStatus` | agent list/detail and realtime invalidation expose `pending|accepted|queued|ready|failed` plus a sanitized failure code |

See `docs/agent-creation-proposal-cutover.md` for the production migration
preflight and post-deploy verification commands.

## Env — `server/internal/handler/agent_env.go`

| Contract | Behavior |
|---|---|
| `GET /api/agents/{id}/env` | owner/admin; agent actors denied |
| `PUT /api/agents/{id}/env` | owner/admin; audited full map replace |

## Skill binding — `server/internal/handler/skill.go`

| Contract | Behavior |
|---|---|
| `GET /api/agents/{id}/skills` | list bindings (`ListAgentSkills`) |
| `POST /api/agents/{id}/skills/add` | additive (`AddAgentSkills`) |
| `PUT /api/agents/{id}/skills` | replace-all (`SetAgentSkills`); empty clears |

## Templates (not a supported create path)

CLI never exposed `--from-template` after prior cuts. Template backend
(`server/internal/agenttmpl/`, `GET /api/agent-templates`,
`POST /api/agents/from-template`) remains orphaned plumbing. Onboarding uses
plain `POST /api/agents`. Do not teach templates as a create path.

## Durable avatar

See migrations 203 and 314, `internal/agentavatar/catalog.go`, `agent.sql`
avatar tuple write, and `agent_avatar_test.go` / migrate fixture tests for
assigned/picked/uploaded, identity-preserving legacy preset migration, and
raw-URL rejection.
