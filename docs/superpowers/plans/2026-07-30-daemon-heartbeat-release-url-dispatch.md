# Plan: daemon heartbeat release-URL dispatch (task #815 step 2)

Design: `docs/superpowers/specs/2026-07-30-daemon-heartbeat-release-url-dispatch-design.md`

## Task 1 — wire field (protocol + server)
- Add `ReleaseManifestBaseURL string \`json:"release_manifest_base_url,omitempty"\`` to `DaemonHeartbeatAckPayload` (`server/pkg/protocol/messages.go`).
- In `processHeartbeat` (`server/internal/handler/daemon.go:1085`), populate it from a new server-side env var (e.g. `MULTICA_SERVER_RELEASE_MANIFEST_BASE_URL`), empty if unset.
- RED: test asserting the ack carries the env value when set / omits when unset. GREEN: implement. Focused `go test ./internal/handler -run TestDaemonHeartbeat...`.

## Task 2 — daemon-side cache
- Add `serverReleaseManifestBaseURL atomic.Value` to `Daemon` struct (`server/internal/daemon/daemon.go`).
- In `handleHeartbeatActions` (`daemon.go:1618`), store non-empty `ReleaseManifestBaseURL` from the ack; leave existing value alone if the field is empty on a given ack.
- RED: test that a later empty-field ack doesn't clobber a previously cached value. GREEN: implement. Focused `go test ./internal/daemon -run TestHandleHeartbeatActions...`.

## Task 3 — cli precedence
- Add `releaseManifestBaseURLWithOverride(serverDispatched string) string` in `server/internal/cli/update.go`, keep zero-arg `releaseManifestBaseURL()` calling it with `""`.
- Wire `auto_update.go`'s `tryAutoUpdate` to pass `d.serverReleaseManifestBaseURL.Load()`.
- RED: table test covering server/env/neither precedence, explicit "server wins over env" case. GREEN: implement. Focused `go test ./internal/cli -run TestReleaseManifestBaseURL...`.

## Task 4 — full verification
- `go build ./...`, `go vet ./...`, full `go test ./internal/daemon/... ./internal/cli/... ./internal/handler/... -count=1`.
- `git diff --check`.
- Open PR, post to `#prj-daemon` with design doc link.

Explicitly out of scope (per design doc §4): `cmd_update.go`'s one-shot `multica update` process reading a persisted local cache. Do not implement unless asked.
