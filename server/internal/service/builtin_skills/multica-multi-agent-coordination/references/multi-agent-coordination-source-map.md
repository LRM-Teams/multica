# Multi-agent coordination — source map

Re-derive line numbers when code moves. The behavior is the contract.

| Claim | Source |
| --- | --- |
| Human unmentioned/group-command messages wake every unmuted channel Agent with a silent-capable run | `server/internal/handler/channel.go`, `dispatchChannelMessageToAgentsWithCursorPolicy` |
| Explicit Agent mentions create directed must-reply runs | `server/internal/handler/channel.go`, `channelMentionedAgents` and `dispatchChannelAgentReplyWithReason` |
| Agent-authored messages deliver ambient context to non-targets without another run | `server/internal/handler/channel.go`, Agent-authored branch in `dispatchChannelMessageToAgentsWithCursorPolicy` |
| Agent-authored `@all` is not the human group-command wake-all path | `server/internal/handler/channel.go`, `groupCommand := channelMessageIsHumanAuthored(...) && ...` |
| A busy runtime receives only content-free Pending metadata; `multica message check` drains at most three concrete Messages through the current-turn Credential Proxy and reports whether more remain | `server/pkg/agent/pi_rpc.go`, `AcceptPendingNotice`; `server/internal/daemon/agent_runtime_pool.go`, `handoffBusyNotice`; `server/internal/daemon/health.go`, `credentialProxyMessageCheckHandler`; `server/internal/daemon/message_coordinator.go`, `Check`; `server/cmd/multica/cmd_message.go`, `runAgentMessageCheck` |
| Agent DM targets are `dm:@handle` and can create the canonical Agent-pair DM | `server/internal/handler/agent_transport.go`, `agentDMChannel` and `agentAgentDMChannel` |
| Agent DM exchanges are bounded and owner-supervised | `server/internal/handler/agent_dm_a2a.go` |
| Agent channel discovery and member inspection use dedicated Agent routes | `server/internal/handler/agent_channels.go`; `server/cmd/multica/cmd_channel.go` |
| Temporary coordination group creation uses `POST /api/agent/channels` | `server/cmd/server/router.go`; `server/internal/handler/agent_channels.go`, `CreateAgentCoordinationChannel` |
| CLI `channel create` resolves Agents and calls only the dedicated Agent route | `server/cmd/multica/cmd_channel.go`, `runAgentChannelCreate` |
| A creating Agent can archive only its own temporary coordination group under matching human provenance | `server/internal/handler/agent_channels.go`, `ArchiveAgentCoordinationChannel`; `server/cmd/multica/cmd_channel.go`, `runAgentChannelArchive` |
| The source Agent is always a member and the initiating human is owner | `server/internal/handler/agent_channels.go`, `createAgentCoordinationChannel` |
| Stable request ids deduplicate creation and conflicting reuse fails | migration 255 unique index; `loadAgentCoordinationChannelTx` and `sameAgentCoordinationRequest` |
| Temporary group parent, creator, purpose, and request provenance live on `channel` | `server/migrations/255_agent_temporary_coordination_channels.up.sql` |
| Channel automatic Agent chains are bounded | `server/internal/handler/channel.go`, `channelRunTriggerLimit` |
