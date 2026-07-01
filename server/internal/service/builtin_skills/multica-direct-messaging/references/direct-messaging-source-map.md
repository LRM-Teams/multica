# Direct messaging — source map

Every claim in `SKILL.md` traces to a line below. Re-derive against the current
tree before trusting a line number; the behavior is the contract, the line is a
pointer.

## The `multica dm` command

| Fact | Source |
| --- | --- |
| `dm` command + `--to` / `--message` / `--message-stdin` / `--message-file` flags | `server/cmd/multica/cmd_dm.go` |
| Registered on the root command | `server/cmd/multica/main.go` (`rootCmd.AddCommand(dmCmd)`) |
| Sends `{ "content": ... }` (and optional `{ "to": ... }`) to `POST /api/chat/agent-dm` | `server/cmd/multica/cmd_dm.go` (`runDM`) |
| Inline `--message` decodes `\n` / `\t` (stdin / file kept verbatim) | `server/cmd/multica/cmd_issue.go` (`resolveTextFlag`) → `server/internal/util/text.go` (`UnescapeBackslashEscapes`) |

## The server endpoint

| Fact | Source |
| --- | --- |
| Route `POST /api/chat/agent-dm` (workspace-member group) | `server/cmd/server/router.go` |
| Agent-only: a non-agent (human/member) actor is rejected with 403 | `server/internal/handler/chat_agent_dm.go` (`resolveActor` guard) |
| Recipient = explicit `to` workspace member, else task initiator, else the agent's owner; must be a human member | `server/internal/handler/chat_agent_dm.go` (`resolveDMRecipient`) |
| Unknown explicit recipient → 404; no default human recipient → 400 telling the agent to use a channel + @-mention | `server/internal/handler/chat_agent_dm.go` (`resolveDMRecipient` / handler) |
| Reuses the most-recent active (human, agent) session, else creates one | `server/internal/handler/chat_agent_dm.go` |
| Inserts a `role='assistant'` chat_message, stamps `unread_since`, touches session | `server/internal/handler/chat_agent_dm.go` (`CreateChatMessage` / `SetUnreadSinceIfNull` / `TouchChatSession`) |
| Broadcasts `chat:message` so the recipient's DM panel / FAB updates live | `server/internal/handler/chat_agent_dm.go` (`publishChat` / `protocol.EventChatMessage`) |

## Tests

| Case proven | Source |
| --- | --- |
| Agent DM creates an assistant message + stamps unread for the task initiator | `server/internal/handler/chat_agent_dm_test.go` (`TestAgentDirectMessage_CreatesDMForInitiator`) |
| Explicit `to` can target a workspace member by name | `server/internal/handler/chat_agent_dm_test.go` (`TestAgentDirectMessage_CreatesDMForExplicitWorkspaceMember`) |
| Unknown explicit recipient is rejected with 404 | `server/internal/handler/chat_agent_dm_test.go` (`TestAgentDirectMessage_RejectsUnknownExplicitRecipient`) |
| A human / member caller is rejected with 403 (DMs are agent-only) | `server/internal/handler/chat_agent_dm_test.go` (`TestAgentDirectMessage_RejectsHumanCaller`) |
