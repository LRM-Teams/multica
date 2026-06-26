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
| Prints a `:sticker:<id>:` token, name, and mood per match | `server/cmd/multica/cmd_sticker.go` (`printStickerTable`) |
| Empty result lists available moods | `server/cmd/multica/cmd_sticker.go` (`runStickerSearch`) → `stickers.Emotions` |

## The sticker library

| Fact | Source |
| --- | --- |
| Catalog (id, bilingual name/tags, mood) is the source of truth | `server/internal/stickers/catalog.json` |
| Images embedded into the binary, kept 1:1 with the catalog | `server/internal/stickers/assets/<id>.png`, embedded in `server/internal/stickers/stickers.go` |
| Assets are Microsoft Fluent Emoji (MIT) | `server/internal/stickers/NOTICE` |
| Search matches id / name / english name / mood / tags, mood-exact first | `server/internal/stickers/stickers.go` (`Search`, `stickerMatches`) |

## Serving + rendering

| Fact | Source |
| --- | --- |
| Public `GET /api/stickers` (catalog) and `GET /api/stickers/{id}` (image) | `server/cmd/server/router.go`; `server/internal/handler/sticker.go` (`ListStickers`, `GetStickerAsset`) |
| Unknown id 404s with no filesystem read (no path traversal) | `server/internal/stickers/stickers.go` (`Asset`) |
| `:sticker:<id>:` in message content renders as the sticker image | `packages/views/common/markdown.tsx` (sticker token handling) |
| Unknown id renders as nothing (graceful) | `packages/views/common/markdown.tsx` (sticker token handling) |

## Tests

| Case proven | Source |
| --- | --- |
| Catalog parses and is 1:1 with the embedded assets | `server/internal/stickers/stickers_test.go` |
| `Asset` returns bytes for a known id and 404s an unknown / traversal id | `server/internal/stickers/stickers_test.go` |
| `Search` matches by mood and keyword in zh + en | `server/internal/stickers/stickers_test.go` |
| `:sticker:<id>:` token renders an image; unknown id renders nothing | `packages/views/common/markdown.test.tsx` |
