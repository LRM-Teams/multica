# Creating agents — source map

Evidence layer for `SKILL.md`. Every contract maps to `file:line` on the
current tree (branch `feat/builtin-skills`, latest `main` merged), the runtime
effect, and a safe read-only check. Line numbers were re-derived against this
tree — re-derive again if the files move, the surrounding context (not the
number) is the anchor.

## Verification

```bash
# Conformance eval for this skill (and the shared template invariants):
go test ./internal/service -run TestCreatingAgentsSkillCoversAgentCreationContracts
go test ./internal/service -run TestBuiltinSkillsConformToTemplate
```

## CLI entry points — `server/cmd/multica/cmd_agent.go`

| Contract | Line | Behavior | Safe check |
|---|---|---|---|
| Create flags: `name`, `description`, `instructions`, `runtime-id` | 159–162 | Registered create flags; `name`/`runtime-id` enforced in `runAgentCreate` | `multica agent create --help` |
| `runtime-config`, `model`, `custom-args` flags | 163–165 | `model` help: "Prefer this over passing --model in --custom-args"; `custom-args` help names codex/openclaw rejecting `--model` (CLI help only, not server-enforced) | `multica agent create --help` |
| Secret-safe env input: `custom-env`, `custom-env-stdin`, `custom-env-file` | 166–168 | `--custom-env` warns about shell history / `ps`; stdin and file modes keep secrets off the command line; mutually exclusive | `multica agent create --help` |
| Secret-safe MCP input: `mcp-config`, `mcp-config-stdin`, `mcp-config-file` (create) | 169–171 | Same three-channel pattern as `custom-env`; `--mcp-config` warns about shell history / `ps`; value must be a JSON object or `null` | `multica agent create --help` |
| MCP flags on `agent update` | 192–194 | Same three channels on update; `--mcp-config null` clears. Unlike `custom_env`, `mcp_config` IS settable via update | `multica agent update --help` |
| `runAgentCreate` builds body + `POST /api/agents` | 414 | Only sets a body key when the flag `Changed`; posts to `/api/agents` (line 482) | read 414–489 |
| Body assembly: description/instructions/runtime-config/custom-args/custom-env/mcp-config/model | 437–478 | `resolveCustomEnv` (455) and `resolveMcpConfig` (460) gate their secret channels; omitted flags are not sent | read 437–478 |
| `runAgentUpdate` sends `mcp_config` | 550 | `resolveMcpConfig` adds `mcp_config` to the `PUT /api/agents/{id}` body (564); `custom_env` is intentionally not a flag here | read 495–565 |
| `parseMcpConfig` / `resolveMcpConfig` helpers | 1066, 1094 | Validator (object-or-`null`, content-free errors) + three-channel resolver, mirroring `parseCustomEnv`/`resolveCustomEnv` | read 1066–1150 |
| `agent skills set` = replace-all | 772 | `PUT /api/agents/{id}/skills` (790); `--skill-ids ''` clears all (779) | `multica agent skills set --help` |
| `agent skills add` = additive | 797 | `POST /api/agents/{id}/skills/add` (818); requires ≥1 id (804, 808) | `multica agent skills add --help` |
| `agent skills list` | 740 | reads bindings, no side effect | `multica agent skills list --help` |
| `agent env get` | 874 | `GET /api/agents/{id}/env` | `multica agent env get --help` |
| `agent env set` | 909 | `PUT /api/agents/{id}/env` with full `custom_env` map (923, 929) | `multica agent env set --help` |

Note: the CLI no longer exposes `--from-template`. The agent-template backend
still exists (registry `server/internal/agenttmpl/`, handler `agent_template.go`,
routes `GET /api/agent-templates` and `POST /api/agents/from-template`, plus the
`packages/core` client/query wrappers) but is currently orphaned plumbing with no
live caller: the removed CLI flag was its only non-test consumer, and onboarding
does NOT use it — `packages/views/onboarding/steps/step-agent.tsx` builds four
hardcoded local presets (i18n-resolved) and creates via plain `POST /api/agents`
(`createAgent`), never `POST /api/agents/from-template`. Do not treat the template
API as a supported agent-creation path. This skill teaches manual `agent create`
only.

## Create handler — `server/internal/handler/agent.go`

| Contract | Line | Behavior |
|---|---|---|
| `maxAgentDescriptionLength = 255` | 31 | Cap is 255 **Unicode code points** (comment: counted via `utf8.RuneCountInString`, matches Postgres `char_length`) |
| `AgentResponse` avatar truth + no plaintext `custom_env` | 36–79, 119–147 | Exposes persisted `avatar_url` and server-owned `avatar_source`, plus only env metadata (`has_custom_env`, `custom_env_key_count`); comment cites MUL-2600 |
| `CreateAgentRequest` fields | 787–813 | `username`, `display_name`, `description`, `instructions`, verified `avatar_selection`, `runtime_config`, `custom_env`, `custom_args`, `model`, `thinking_level` (plus visibility/mcp_config/max_concurrent_tasks) |
| identity seed required | 654–658 | Create accepts old `name` or new `display_name`; both empty → 400 "name or display_name is required" |
| `description` ≤ 255 code points | 660–662 | `utf8.RuneCountInString(req.Description) > maxAgentDescriptionLength` → 400 |
| `runtime_id` required | 664–666 | `if req.RuntimeID == ""` → 400 "runtime_id is required" |
| `runtime_id` must resolve in workspace | 675–690 | parsed + `GetAgentRuntimeForWorkspace`; unknown → 400 "invalid runtime_id" |
| `thinking_level` provider-level validation | 702–708 | `!agent.IsKnownThinkingValue(runtime.Provider, req.ThinkingLevel)` → 400; per-model gaps deferred to daemon (comment 702–705, MUL-2339) |
| Defaults: `{}` config/env, `[]` args | 721–734 | `RuntimeConfig`→`{}`, `CustomEnv`→`{}`, `CustomArgs`→`[]` when nil, before insert |
| `visibility` default | 668–670 | `if req.Visibility == "" { req.Visibility = "private" }` — access-control field, not the runtime prompt |
| `max_concurrent_tasks` default | 671–672 | `if req.MaxConcurrentTasks == 0 { req.MaxConcurrentTasks = 6 }` — scheduler cap |
| `mcp_config` null-skip on create | 736–739 | raw JSON copied through unless the body value is the literal `null` |
| `mcp_config` redacted on read | 56, 573–590 | `redactMcpConfig` sets `McpConfigRedacted=true`; agent actors and private/unauthorized member reads redact |
| Avatar verification on create | `server/internal/handler/agent.go:839-852,945-991`; `server/internal/handler/agent_avatar.go:45-148` | omit lets migration 203 assign; `uploaded` resolves an owned workspace image attachment and uses its DB URL; `picked` accepts only a canonical preset; raw `avatar_url` is rejected |
| `CreateAgent` insert params | 975–1008 | persists generated handle/display_name plus the server-derived avatar tuple, runtime_config, instructions, custom_env, custom_args, model, thinking_level, mcp_config, visibility, max_concurrent_tasks |
| `UpdateAgent` rejects `custom_env` | 929–938 | if `custom_env` present in body → 400 "use PUT /api/agents/{id}/env (or `multica agent env set`)" |
| `UpdateAgent` treats `name` as display rename | 944–958 | old `name` and new `display_name` update only `display_name`; the stable handle remains unchanged |
| `description` ≤ 255 on update too | 960–964 | same cap re-checked on update |
| Avatar update fail-closed | `server/internal/handler/agent.go:1279-1292,1337-1341`; `server/internal/handler/agent_avatar.go:45-148` | omit leaves the tuple unchanged; a verified selection updates URL/source/attachment atomically; raw `avatar_url` is rejected |

## Durable avatar write boundary — migration/query/tests

| Contract | Source | Behavior |
|---|---|---|
| Three-state persisted schema | `server/migrations/203_agent_durable_avatar.up.sql:17-27,52-62` | adds NOT NULL `avatar_source`, an optional attachment FK, source/attachment consistency CHECK, and one-attachment-per-agent uniqueness; preserves every historical nonblank URL byte-for-byte as `assigned`, fills only blank/NULL rows, then makes `avatar_url` NOT NULL |
| Every insert receives one durable assigned value | `server/migrations/203_agent_durable_avatar.up.sql:29-50` | BEFORE INSERT trigger assigns one concrete preset plus `assigned`/NULL attachment only when URL is blank; verified explicit tuples remain intact |
| Picker/upload provenance is atomic with URL | `server/pkg/db/queries/agent.sql:19-64` | create writes the verified tuple; update uses one `avatar_selection_set` CASE across URL/source/attachment, with no URL-shape classification |
| Behavioral regression matrix | `server/internal/handler/agent_avatar_test.go:19-559` | covers assigned create, verified picked/uploaded create+update, raw URL and invalid attachment rejection, trusted-draft remapping, direct inserts, concurrent creates, attachment uniqueness, and atomic concurrent tuple updates |
| Executable migration fixture | `server/cmd/migrate/agent_avatar_migration_test.go:20-187` | applies the real 203 SQL against a temporary PostgreSQL schema, checks byte preservation/backfill/constraints/direct insert/attachment uniqueness, then applies down |

## Env endpoint — `server/internal/handler/agent_env.go`

| Contract | Line | Behavior |
|---|---|---|
| `authorizeAgentEnv` gate | 66 | loads agent, then applies the two checks below |
| Agent actors denied | 80–84 | `if actorType == "agent"` → 403 "agents may not access env management endpoints" (MUL-2600 impersonation guard) |
| Owner/admin only | 86 | `requireWorkspaceRole(..., "owner", "admin")` |

## Routes — `server/cmd/server/router.go`

| Contract | Line | Behavior |
|---|---|---|
| `GET /env` | 603 | `h.GetAgentEnv` (plaintext read, gated) |
| `PUT /env` | 604 | `h.UpdateAgentEnv` (full-map overwrite, gated) |

## Claim-time injection — `server/internal/handler/daemon.go`

| Contract | Line | Behavior |
|---|---|---|
| Fresh agent re-read on claim | 1109–1111 | `GetAgent(task.AgentID)` — claim uses persisted fields, not create output |
| Workspace skills FIRST | 1115 | `skills := h.TaskService.LoadAgentSkills(...)` |
| Built-ins appended | 1116 | `skills = append(skills, h.TaskService.BuiltinSkills()...)` |
| Runtime payload | 1130–1143 | `TaskAgentData` carries `Instructions`, `Skills`, `CustomEnv`, `CustomArgs`, `Model`, `ThinkingLevel`, `McpConfig` (1130–1131, 1140) — confirms these are runtime-consumed; `description`, `visibility`, and `max_concurrent_tasks` are absent (not runtime-prompt fields) |

## Skill loading — `server/internal/service/task.go`

| Contract | Line | Behavior |
|---|---|---|
| `LoadAgentSkills` | 1685 | `ListAgentSkills` + per-skill `ListSkillFiles` → content + supporting files for execution |

## Built-in skills — `server/internal/service/builtin_skills.go`

| Contract | Line | Behavior |
|---|---|---|
| `go:embed builtin_skills` | 10–11 | skills embedded at compile time |
| `loadBuiltinSkill` | 45 | reads `<name>/SKILL.md` (47) + walks sibling files into `Files` (56–68) |

## Persisted columns — `server/pkg/db/generated/agent.sql.go`

| Contract | Line | Behavior |
|---|---|---|
| `CreateAgent` INSERT | 820–826 | columns include `name, display_name, runtime_config, runtime_id, instructions, custom_env, custom_args, mcp_config, model, thinking_level` |
| `CreateAgentParams` | 829–847 | typed params: `Name string`, `DisplayName string`, `RuntimeConfig []byte`, `Instructions string`, `CustomEnv []byte`, `CustomArgs []byte`, `Model pgtype.Text`, `ThinkingLevel pgtype.Text` |
| `UpdateAgent` SET | 2673–2692 | COALESCE updates of `display_name, runtime_config, instructions, custom_env, custom_args, model, thinking_level` — note `custom_env` is COALESCE-guarded but the handler rejects it before this query runs |
| `UpdateAgentCustomEnv` (called by the `UpdateAgentEnv` handler) | 2652 | `SET custom_env = $2` — the only write path for env values |
