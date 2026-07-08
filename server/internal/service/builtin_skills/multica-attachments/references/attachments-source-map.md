# Attachments — source map

Every claim in `SKILL.md` traces to a line below. Re-derive against the current
tree before trusting a line number; the behavior is the contract, the line is a
pointer.

## The `multica send --attachment` flag

| Fact | Source |
| --- | --- |
| `--attachment` registered on `multica send` as a repeatable flag | `server/cmd/multica/cmd_message.go` (`sendCmd.Flags().StringSlice("attachment", ...)`) |
| `runAgentMessageSend` requires message, sticker, or attachment (not none) | `server/cmd/multica/cmd_message.go` (`runAgentMessageSend`) |
| Upload happens client-side before the send POST; URL-shaped `--attachment` values are skipped with a stderr warning, not uploaded | `server/cmd/multica/cmd_issue.go` (`uploadCLIAttachments`, `isHTTPURL`) |
| Longer client timeout (>= 60s) is applied automatically whenever `--attachment` is present | `server/cmd/multica/cmd_message.go` (`runAgentMessageSend`, `cli.AtLeastAPITimeout`) |
| `--target`, `--sticker`, `--show-in-channel` compose with `--attachment` in the same send call | `server/cmd/multica/cmd_message.go` (`runAgentMessageSend` builds one request body from all flags) |

## Server-side linking

| Fact | Source |
| --- | --- |
| Uploaded file IDs are sent as `attachment_ids` on the transport-send request | `server/internal/handler/agent_transport.go` (`AgentTransportSendRequest.AttachmentIDs`) |
| IDs are validated as UUIDs before use | `server/internal/handler/agent_transport.go` (`AgentTransportSendMessage`, `parseUUIDSliceOrBadRequest`) |
| Attachments are bound to the resolved channel + newly created message inside the same transaction as the insert, scoped to this agent's own uploads | `server/internal/handler/agent_transport.go` (`createAgentTransportMessage`, `insertAgentTransportMessageWithAudit`, `LinkOwnedAttachmentsToChannelMessage`) |
| The linking query only matches attachments this agent uploaded and hasn't already attached elsewhere — an attachment ID from another actor is silently left unlinked, not attached | `server/pkg/db/queries/attachment.sql` (`LinkOwnedAttachmentsToChannelMessage`) |
| The upload endpoint itself doesn't require knowing the target channel up front — the CLI uploads unbound, since `--target` is resolved server-side | `server/internal/handler/agent_transport.go` (`resolveAgentTransportTarget`) |

## Tests

| Case proven | Source |
| --- | --- |
| An agent's own uploaded attachment gets linked to its sent message; a foreign attachment ID is left unlinked | `server/internal/handler/agent_transport_test.go` (`TestAgentTransportSendMessageLinksOwnedAttachmentsOnly`) |
