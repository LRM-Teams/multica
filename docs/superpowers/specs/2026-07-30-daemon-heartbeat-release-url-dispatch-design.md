# Daemon release-manifest URL: server-dispatched layer (task #815 step 2)

Status: draft, implementing today. Owner: Nash. Related: #815, PR #1526
(env var layer, merged), the 2026-07-30 ICP-block incident that motivated this.

## ⚠️ Scope limit — read before assuming this fixes manual installs

**This only covers the daemon's own unattended auto-update loop.** It does
**not** cover a human running `install.sh` or `multica update` by hand —
`multica update` is a fresh one-shot process with no daemon connection, so it
has no way to receive the server-dispatched value (see §4). Those manual
paths still only see the `MULTICA_RELEASE_MANIFEST_BASE_URL` env var / the
compile-time default, exactly as before this change. Concretely: after this
ships, "the domain is blocked, did we fix it for everyone" is **not** a yes —
it's "unattended already-running daemons self-heal automatically; anyone
installing or manually upgrading still needs the env var set by hand." Do not
conflate the two when answering that question (Parker, 2026-07-30 18:38,
`#prj-daemon`, after today's install.sh/ICP incident was specifically the
manual path).

## Problem

The daemon's release-manifest base URL (where it downloads CLI update
artifacts from) has two layers today: a compile-time constant
`DefaultReleaseManifestBaseURL` and an env var override
`MULTICA_RELEASE_MANIFEST_BASE_URL` (`server/internal/cli/update.go`, PR
#1526). Both require touching every machine by hand (SSH in and export a var,
or ship a new release and get everyone to reinstall) whenever the download
domain becomes unreachable — exactly what happened today when `leagent.me`
got ICP-blocked on some networks. Frank's call: the address should be
**server-dispatched** — the daemon already talks to the server continuously,
so it should ask the server where to download from rather than carry a
baked-in address that only changes via redeploy. Precedent: Raft's own
Computer does compile-time-default + env-var-override; server-dispatch goes
one step further than Raft, which Frank explicitly wants.

## Chosen channel: heartbeat ack

Researched and rejected two alternatives before landing here (see thread
`#prj-daemon:0021d1ab` for the live research request/response this doc is
based on):

- **Workspace `settings` blob** (`RegisterResponse.settings`, refreshed via
  `GetWorkspaceRepos`/`refreshWorkspaceRepos`, `daemon.go:1161`): rejected —
  it's keyed per-workspace (`d.workspaces[workspaceID].settings`), but the
  release-manifest URL is a daemon-*process* concern (one CLI binary gets
  updated regardless of how many workspaces this daemon serves). A daemon
  with zero registered workspaces would never see it, and multi-workspace
  daemons would have an undefined "which workspace's settings win" hazard.
- **execenv `runtime_config.go`/`startup_digest.go`**: rejected — not a
  network channel at all. Pure local file-templating (prompt
  briefs) and a zero-I/O fingerprint function over already-in-process task
  data. No poll/fetch happens underneath either.

**Heartbeat is the right fit**: `SendHeartbeat` (`client.go:552`) POSTs to
`/api/daemon/heartbeat` every `d.cfg.HeartbeatInterval` (default 15s,
`config.go:24`), server-side funneled through `processHeartbeat`
(`handler/daemon.go:1085`). `DaemonHeartbeatAckPayload`
(`protocol/messages.go:626-658`) already carries server→daemon pending-action
fields — `PendingUpdate.TargetVersion` is precedent for exactly this shape of
value flowing into the same `cli` package. It's process-scoped (top-level
field, not nested in a per-workspace map), ticks fast enough that a domain
change propagates within one heartbeat interval, and per Parker's explicit
constraint (2026-07-30 18:08, `#prj-daemon`): the value must be refreshable
while the daemon runs (not just fetched once at register/startup), and no new
dedicated endpoint should be opened if heartbeat can carry it. It can.

## Design

### 1. Wire field

`protocol.DaemonHeartbeatAckPayload` gets one new optional top-level field:

```go
ReleaseManifestBaseURL string `json:"release_manifest_base_url,omitempty"`
```

Server populates it in `processHeartbeat` (or wherever the ack is assembled)
from a server-side env var, e.g. `MULTICA_SERVER_RELEASE_MANIFEST_BASE_URL`
(distinct name from the daemon-side one added in #1526 — this is the
*server's* config of what to tell daemons, not the daemon's own override).
Empty/unset means "server has no opinion," daemon falls through to its
existing env-var/default precedence. No DB table needed for v1 — this is a
single global value, not per-workspace/per-daemon, and an env var restart is
an acceptable admin action for changing it (much cheaper than the
alternative it replaces: reinstalling every machine).

### 2. Daemon-side caching

Add a mutex-guarded field on `Daemon` (same pattern as other heartbeat-derived
state, e.g. `updateObservation`):

```go
serverReleaseManifestBaseURL atomic.Value // holds string, "" = unset
```

Set from `handleHeartbeatActions` (`daemon.go:1618`) whenever a heartbeat ack
arrives with a non-empty `ReleaseManifestBaseURL`. `atomic.Value` (not a
plain mutex+field) because reads happen from the auto-update goroutine and
writes from the heartbeat goroutine, and there's no compound
read-then-write — a plain atomic swap/load is sufficient and avoids adding
another `sync.Mutex` field to an already-large struct.

### 3. Precedence in `cli` package

`releaseManifestBaseURL()` (`internal/cli/update.go:48`) today takes no
arguments and reads `os.Getenv` directly. Change its call sites (not its
internals) to pass an optional override:

```go
func releaseManifestBaseURLWithOverride(serverDispatched string) string {
    if v := strings.TrimSpace(serverDispatched); v != "" {
        return v
    }
    if v := strings.TrimSpace(os.Getenv(ReleaseManifestBaseURLEnv)); v != "" {
        return v
    }
    return DefaultReleaseManifestBaseURL
}
```

Keep the existing zero-arg `releaseManifestBaseURL()` calling this with `""`
(so `ReleaseWebURL()` and any caller with no daemon context keep working
unchanged) — only `auto_update.go`'s daemon-owned call site passes
`d.serverReleaseManifestBaseURL.Load()`.

### 4. The `cmd_update.go` one-shot gap (scoped OUT of v1)

`multica update` (`cmd_update.go:26`) is a fresh process with no daemon
connection — it cannot read the daemon's in-memory cached value. Solving this
would need the daemon to persist the last-seen server value to a local file
(e.g. alongside the existing daemon state dir) that `runUpdate` reads on
startup. **Not doing this in v1**: the daemon-owned `auto_update.go` path is
what actually matters for the incident this is fixing (unattended machines
recovering without human intervention); a human running `multica update`
manually already has hands on the keyboard and can pass
`MULTICA_RELEASE_MANIFEST_BASE_URL` inline if needed. Revisit only if a
concrete complaint surfaces.

## Test strategy

- **Server**: `processHeartbeat`/ack-building test asserting
  `ReleaseManifestBaseURL` is populated from the server-side env var when set,
  and omitted (empty, `omitempty` drops it) when unset.
- **Daemon**: focused test on `handleHeartbeatActions` asserting the cached
  atomic value updates when an ack carries a non-empty URL, and is left alone
  (not reset to `""`) when a later ack omits the field — a transient
  server-side hiccup shouldn't blank out a previously-good value.
- **cli package**: table test on `releaseManifestBaseURLWithOverride` covering
  all three precedence combinations (server set / env set / neither) plus the
  "server wins over env" case explicitly, since that's the one behavior this
  whole change exists to add.
- No live end-to-end test (would require a running server + daemon pair);
  covered by the existing manual s144-style verification pattern used for
  #1526 today if wanted, not required for merge.
