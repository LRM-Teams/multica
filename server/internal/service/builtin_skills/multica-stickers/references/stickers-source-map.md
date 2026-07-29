# Stickers — source map

Every claim in `SKILL.md` traces to a line below. Re-derive against the current
tree before trusting a line number; the behavior is the contract, the line is a
pointer.

## The `multica sticker` command

| Fact | Source |
| --- | --- |
| `sticker` parent + `search` / `list` subcommands | `server/cmd/multica/cmd_sticker.go` |
| Registered on the root command | `server/cmd/multica/main.go` (`rootCmd.AddCommand(stickerCmd)`) |
| Search by keyword in zh or en; reads the embedded catalog (offline, no API) | `server/cmd/multica/cmd_sticker.go` (`runStickerSearch`) → `server/internal/stickers/stickers.go` (`Search`) |
| Prints sticker id, name, and mood per match | `server/cmd/multica/cmd_sticker.go` (`printStickerTable`) |
| Empty result lists available moods | `server/cmd/multica/cmd_sticker.go` (`runStickerSearch`) → `stickers.Emotions` |

## The sticker library

| Fact | Source |
| --- | --- |
| Catalog (id, bilingual name/tags, mood, file) is the source of truth; embeds only catalog.json so the CLI stays lean | `server/internal/stickers/catalog.json`, `server/internal/stickers/stickers.go` |
| Image bytes embedded separately (server-only), served by filename, 1:1 with the catalog | `server/internal/stickerimg/files/`, `server/internal/stickerimg/stickerimg.go` (`Read`, `Names`) |
| Assets are from getActivity/EmojiPackage (Apache-2.0) | `server/internal/stickerimg/NOTICE` |
| Search matches id / name / english name / mood / tags, mood-exact first | `server/internal/stickers/stickers.go` (`Search`, `stickerMatches`) |

## Serving + rendering

| Fact | Source |
| --- | --- |
| Public `GET /api/stickers` (catalog) and `GET /api/stickers/{id}` (image) | `server/cmd/server/router.go`; `server/internal/handler/sticker.go` (`ListStickers`, `GetStickerAsset`) |
| Unknown id 404s with no filesystem read (no path traversal) | `server/internal/stickers/stickers.go` (`Asset`) |
| Agent transport send accepts structured `parts[]` including stickers and attachments | `server/internal/handler/agent_transport.go` (`AgentTransportSendMessage`), `server/cmd/multica/cmd_message.go` (`runAgentMessageSend`, `buildAgentSendParts`, `--sticker`, `--attachment-id`) |
| CLI `--attachment-id` becomes `{type:attachment, attachment_id}` parts before POST; chat send does not use sidecar `attachment_ids` or markdown image embeds | `server/cmd/multica/cmd_message.go` (`buildAgentSendParts`, `runAgentMessageSend`); server binds from attachment parts only in `agent_transport.go` / `channel.go` (`attachmentIDsFromParts`) |
| Formal P0 messages reference stickers through the `send` action body with structured `parts[]` and `sticker_id`; legacy `message_send` remains accepted at the server boundary | `server/pkg/protocol/messages.go` (`ChatOutputActionMessageSend`, `MessagePart`), `server/internal/messageparts/messageparts.go`, `server/internal/handler/daemon.go` (`normalizeTaskCompleteOutput`) |
| Runs without `ChannelID` deliver final assistant output automatically; a structured sticker envelope is unwrapped into durable message parts, while runs with `ChannelID` retain the explicit CLI transport path | `server/internal/daemon/prompt.go` (`buildChatPrompt`), `server/internal/daemon/execenv/runtime_config.go` (`renderChatRuntimeBrief`), `server/internal/service/task.go` (`CompleteTask` chat persistence), `server/internal/messageparts/messageparts.go` (`UnwrapStructuredMessageSend`) |
| Legacy markdown still contains token parsing until the FE renderer sweep removes it | `packages/views/common/markdown.tsx` (legacy sticker token handling) |

## Tests

| Case proven | Source |
| --- | --- |
| Catalog parses and is 1:1 with the embedded assets | `server/internal/stickers/stickers_test.go` |
| `Read` returns bytes for a known file and rejects unknown / traversal names | `server/internal/stickerimg/stickerimg_test.go` |
| `Search` matches by mood and keyword in zh + en | `server/internal/stickers/stickers_test.go` |
| Standalone sticker envelopes unwrap to accessible structured parts and the runtime brief/skill select delivery by channel binding | `server/internal/messageparts/messageparts_test.go`, `server/internal/daemon/prompt_test.go`, `server/internal/daemon/execenv/runtime_config_test.go`, `server/internal/service/builtin_skills_test.go` |
| Legacy token parser remains covered until FE migration removes it | `packages/views/common/markdown.test.tsx` |
