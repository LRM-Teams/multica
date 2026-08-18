# Raft Computer 1.0.16 Runtime Process Architecture and Multica Alignment

**Research date:** 2026-08-17  
**Subject:** the official `raft-computer` 1.0.16 Linux x64 distributable, not merely the daemon npm package

## Scope, method, and confidence

This report is a static architecture reconstruction, not a claim about behavior observed on a live Raft server. Its primary evidence is `/tmp/raft-computer-1.0.16.strings`, extracted from `/tmp/raft-official-install/raft-computer`. The local executable hashes to `d46c2f1a76bded81faf1b887ed26604ac0094b75eed10c634027e562bbfbd4f7`, exactly matching the official 1.0.16 manifest's `linux-x64` SHA-256. `file` identifies it as an x86-64 Linux ELF. The manifest also publishes macOS arm64/x64, Linux arm64, and Windows x64 targets under the same product/version.

The installer identifies this as a self-contained single-executable application (SEA), verifies its SHA-256 against the release manifest, and installs it as `raft-computer`. The official Computers documentation likewise describes **Raft Computer** as the local background service that connects one machine to one or more Servers and runs agents. These sources establish one product/distributable. The hidden modes and subprocesses below are **runtime process roles inside that product**, not separate products.

Bundle locations below are line numbers in the supplied strings file. It retains original module labels such as `packages/computer/src/service.ts`; these are useful provenance inside the verified binary, but are not public source-code links. “Not found” conclusions mean no corresponding implementation was identified in this complete extracted bundle and should be read with the normal limits of static analysis.

## Verified Raft topology

```text
raft-computer (one installed product / SEA executable)
└─ __service                         machine service/supervisor OS process
   ├─ __run <server A id>            one OS child per attached/managed Server
   │  ├─ DaemonCore                  in-process object inside this __run process
   │  └─ provider/runtime children   OS subprocesses launched by DaemonCore
   └─ __run <server B id>
      ├─ DaemonCore
      └─ provider/runtime children
```

### Service and per-Server runner roles

- `packages/computer/src/service.ts` builds hidden resident modes by re-executing the same SEA. `spawnDetachedService` selects `__service` (`strings:1574640-1574708`).
- `runService` reconciles managed Server IDs. `spawnChild` builds `__run <serverId>`, calls Node `child_process.spawn`, retains the returned child handle, and redirects output to the Server's runner log (`strings:1574950-1575050`). This is one `__run` OS child per desired Server—not one process per agent.
- The service supervises through that process handle: it listens for child `exit`, classifies code/signal, records crashes, applies restart backoff/degraded policy, and clears the runner PID file. Stop/shutdown sends `SIGTERM` through `child.kill` (or `process.kill` for a rehydrated external PID), while service-level stop sends `SIGTERM`, polls liveness, and times out with an explicit force-kill instruction (`strings:1574709-1574860`, `1575050-1575155`, `1575740-1575840`). Thus lifecycle is grounded in process handles, signals, and exit observation rather than a logical in-memory runner alone.
- `runResident` loads the bundled `@botiverse/raft-daemon/core`, constructs `new coreMod.DaemonCore(...)`, then calls `core.start()` in the same `__run` process; SIGTERM/SIGINT invoke `core.stop()` (`strings:1574860-1574950`, `1575280-1575460`). **DaemonCore is in-process inside `__run`; it is not another service child.**
- DaemonCore's runtime implementations launch provider processes (for example Claude/Codex/Cursor and other CLI/SDK runtime hosts) using the bundled process-launch abstractions. These are subprocesses below `__run`, distinct from the Computer service and Server runner roles (representative bundle sites include `strings:1038215`, `1084691-1084955`, `1120849`, and `1142806-1150233`).

This corrects the earlier daemon-tarball-only inference: **`@botiverse/raft-daemon@1.0.16` alone does not contain Computer orchestration.** It supplies daemon/runtime functionality, including `DaemonCore`; the officially distributed Raft Computer binary adds the `__service` supervisor, per-Server `__run` processes, local IPC, state files, startup recovery, and lifecycle routing.

## Durable state and readiness evidence

The path module (`strings:862030-862130`) defines, per Server:

- `runner.state.json`: the persistent attachment (Server ID/slug/machine identity, API key, Server URL, and attachment time), despite the filename sounding like transient lifecycle state;
- `runner.pid`: the current runner process evidence;
- `runner.connected`: a readiness/connection marker;
- `runner.log`, `runner-version.json`, `health.json`, and lifecycle-operation state.

The connected marker stores `{pid, connectedAt}` with mode `0600`. DaemonCore lifecycle hooks create it on connection and remove it on disconnect, but only if its PID belongs to the current process (`strings:890300-890390`, `1575330-1575385`). The service does not mark a spawned child running merely because `spawn` succeeded: `hasRunnerReadyEvidence` requires both a live PID and a `runner.connected` marker for that same PID, then writes `runner.pid` and transitions the runner to running (`strings:1574900-1575035`). Service-wide equivalents include `service.state.json` and `service.pid` under `computer/run/`.

## Local IPC and lifecycle ownership

The service owns a local IPC endpoint at `<SLOCK_HOME>/computer/run/service.sock` on Unix. On Windows it derives `\\.\pipe\raft-computer-<16-hex-hash>` from the Computer state directory (`strings:862105-862120`). Both client and server use Node local sockets/pipes, not loopback HTTP.

Messages are UTF-8 JSON framed by a four-byte **big-endian unsigned length prefix**, with a 1 MiB maximum frame (`packages/computer/src/internal/ipc-codec.ts`, `strings:890210-890270`). The IPC server exposes read operations plus mutations including `restart-service` and `upgrade-start` (`strings:1574900-1574995`). In the `__run` DaemonCore callback, a remote managed-computer restart calls `requestServiceRestartViaIpc`; upgrade calls `requestServiceUpgradeViaIpc`. The comments explicitly assign download/swap/self-restart ownership to the service and not the runner (`strings:1575385-1575455`). CLI live restart/upgrade also route to these service IPC mutations. Therefore **machine restart and machine upgrade mutate the live machine only through the service IPC seam**; they are not performed directly by DaemonCore/provider children.

`machine-attestation` is an IPC request used for bounded status, takeover, and upgrade/replacement validation (`strings:892500-894100`, handler registration at `1574900-1574995`). No timer or reconcile loop periodically asks each Host/runner to attest. The periodic service reconcile checks desired runners and PID/connected readiness, while attestation is request-driven around lifecycle operations. In that precise sense, **Raft Computer 1.0.16 has no periodic Host attest**.

## Startup recovery and residue cleanup

`runService` invokes `runServiceStartupRecovery` before binding IPC or reconciling children (`strings:1574870-1574925`). The full cleanup pass (`packages/computer/src/cleanup.ts`, `strings:891200-891410`) does all of the following:

1. removes dead `service.pid` and per-Server `runner.pid` files after a liveness check;
2. on Unix, reads `ps -o pid,ppid,comm -A`, identifies unrecorded direct children of the recorded service PID whose command begins `slock-`, and sends `SIGTERM` (Windows skips this `ps` orphan pass);
3. quarantines partial per-Server state left by power loss and removes old upgrade temporary files;
4. cleans a stale `<computer>/.lock` only through `proper-lockfile`, with a 60-second stale threshold; if startup inherited the parent mutation lock, it deliberately skips lock cleanup.

This is narrower than arbitrary process-table killing: parent PID, command prefix, known PID exclusions, platform, and signal are all constrained. It nevertheless provides durable PID/orphan recovery absent from an in-memory-only supervisor. The startup log omits the orphan count even though the report records it; `doctor --cleanup` prints the signaled PIDs (`strings:1579360-1579410`).

## Current Multica comparison

| Concern | Raft Computer 1.0.16 | Current Multica evidence | Gap |
| --- | --- | --- | --- |
| Product/process boundary | One executable; `__service` spawns one `__run` OS child per Server | Production injects `BindingRunnerLauncher.Run` and calls `StartInProcessBinding`; `computer __runner` is only the executable fallback (`server/internal/computer/binding_supervisor.go`, `server/internal/computer/inprocess_binding.go`, `server/cmd/multica/cmd_computer_resident.go`) | Production Bindings are goroutines in the Host OS process, not durable children |
| Child lifecycle | Child handle, SIGTERM, exit classification/restart | The fallback already has `exec.Cmd`, `PID`, `Wait`, and `Stop` (`server/internal/computer/binding_child.go`); supervisor lifecycle is abstracted behind `BindingChild` (`server/internal/computer/binding_supervisor.go`) | Existing seam is close, but production does not select it |
| Daemon/runtime placement | DaemonCore in `__run`; provider subprocesses below it | `RunBindingChild` creates the daemon role; providers launch their own OS processes, e.g. Cursor ACP (`server/internal/daemon/binding_child_runtime.go`, `server/pkg/agent/cursor_acp.go`, `server/pkg/agent/acp_client.go`) | Conceptually aligned if `__runner` becomes production |
| Durable runner evidence | attachment `runner.state.json`, PID, connected marker | Binding config is durable, but child identity/readiness is primarily in memory; fallback bootstrap/ready uses stdin/stdout JSON (`server/internal/computer/binding_child_protocol.go`, `server/internal/computer/binding_supervisor.go`) | No equivalent durable per-Binding PID + connected evidence |
| Machine local control | Unix socket / Windows named pipe; 4-byte-BE-length JSON | Host control and child credential/control endpoints use loopback TCP HTTP (`server/internal/computer/host_process.go`, `server/internal/daemon/binding_child_runtime.go`) | No cross-platform local socket/pipe framed protocol |
| Restart/upgrade routing | Service is sole live mutator through IPC | Live upgrade originates in cloud/Binding flow and child-to-Host calls use loopback HTTP; Host exposes shutdown/upgrade handlers (`server/internal/computer/lifecycle_upgrade.go`, `server/internal/computer/binding_machine_upgrade.go`, `server/internal/computer/host_process.go`) | Ownership is service-oriented, but transport and some routing differ |
| Startup residue | stale PID removal, constrained Unix orphan `ps` cleanup, stale lock cleanup | Machine singleton uses advisory `flock`/`LockFileEx`; stale lock files are harmless. Stale PID removal is an explicit doctor fix, not the full automatic startup pass (`server/internal/computer/lease.go`, `server/internal/computer/file_lock_unix.go`, `server/internal/computer/file_lock_windows.go`, `server/internal/computer/diagnose.go`) | No automatic durable child orphan recovery; lock semantics intentionally differ |
| Attestation | Request-driven lifecycle attestation; no periodic Host attest | Attestation is a loopback HTTP endpoint/probe used for successor validation, with no periodic Host polling (`server/internal/computer/machine_attestation.go`, `server/internal/computer/host_process.go`, `server/cmd/multica/machine_upgrade_detached.go`) | Broadly aligned on “no periodic Host attest” |

## Staged alignment difficulty (design estimate; no implementation)

| Stage | Change | Difficulty | Why / completion boundary |
| --- | --- | --- | --- |
| 1 | Switch production `BindingRunnerLauncher` from the injected in-process `Run` path to the existing `computer __runner` fallback | **Easy** | Spawn, bootstrap, ready, PID, process handle, stop, wait, and supervisor restart seams already exist. **This is easy but incomplete**: a Host crash can still lose all authoritative child knowledge, and readiness/state are not durably reconstructed. |
| 2 | Persist per-Binding attachment/runner lifecycle evidence (PID plus connected/readiness identity), atomically update it, and rehydrate supervision safely | **Medium** | Requires a state contract, PID reuse defenses, crash-consistent writes, ownership fencing, and migration/diagnostics decisions. A PID alone must not become kill authority. |
| 3 | Add startup stale-PID and orphan discovery/termination with bounded TERM/wait/KILL and tests for Host crash/restart | **Medium–high** | Unix process-table logic is manageable, but avoiding unrelated-process termination requires command/process-start identity and generation fencing. Windows needs a different process-discovery/handle strategy. |
| 4 | Replace or supplement loopback HTTP with Unix-domain socket and Windows named-pipe IPC using the four-byte-length-prefixed JSON contract; route all live restart/upgrade mutations through it | **Medium–high** | Requires protocol/version/error contracts, framing limits, endpoint permissions, stale socket cleanup, named-pipe security, cancellation, and cross-platform integration tests. |
| 5 | Exact operational parity and hardening | **High** | Combine durable rehydration, orphan recovery, IPC ownership, upgrade handoff, lifecycle receipts, crash budgets, lock interaction, packaging, and platform service managers without split-brain mutation. |

The practical conclusion is deliberately two-part: **switching production to the existing `__runner` is easy, but it is not Raft parity**. Exact parity for durable PID/connected state, orphan recovery, and cross-platform Unix-socket/named-pipe IPC is **medium–high difficulty**, with final upgrade/handoff hardening high. Multica should preserve its OS advisory machine lock rather than copy Raft's stale directory-lock semantics; alignment should target observable ownership and recovery guarantees, not identical lock implementation.

## Primary sources

### Official Raft sources

- Raft Computers documentation: <https://docs.raft.build/features/server/computers>
- Official installer: <https://cdn.raft.build/computer/install.sh>
- Official latest manifest (observed version 1.0.16 on the research date): <https://cdn.raft.build/computer/manifest.json>
- Verified local binary: `/tmp/raft-official-install/raft-computer`
- Primary extracted bundle evidence: `/tmp/raft-computer-1.0.16.strings`

### Multica source paths inspected

- `server/internal/computer/binding_child.go`
- `server/internal/computer/binding_child_protocol.go`
- `server/internal/computer/binding_supervisor.go`
- `server/internal/computer/inprocess_binding.go`
- `server/internal/computer/host_process.go`
- `server/internal/computer/machine_attestation.go`
- `server/internal/computer/lifecycle_upgrade.go`
- `server/internal/computer/binding_machine_upgrade.go`
- `server/internal/computer/lease.go`
- `server/internal/computer/file_lock_unix.go`
- `server/internal/computer/file_lock_windows.go`
- `server/internal/computer/diagnose.go`
- `server/internal/daemon/binding_child_runtime.go`
- `server/cmd/multica/cmd_computer.go`
- `server/cmd/multica/cmd_computer_resident.go`
- `server/cmd/multica/machine_upgrade_detached.go`
- `server/pkg/agent/cursor_acp.go`
- `server/pkg/agent/acp_client.go`
