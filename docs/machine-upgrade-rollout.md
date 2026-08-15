# Machine Upgrade rollout and rollback

Machine Upgrade is an additive, Computer-scoped lifecycle. Artifact publication
and ephemeral scratch staging are progress only; the installed product is the
on-PATH Computer. Server completion requires a capable successor generation plus
recovery of the Runtime set captured across every active Workspace Execution
Binding at acceptance.
Only the Computer owner may initiate or cancel this machine-wide mutation. A
Workspace supplies visibility and an entry point, not an independent upgrade
scope; every active Workspace connection projects the same operation and result.
The server announces projection changes with `computer:updated` carrying only
the stable `computer_id`; each Workspace client refetches its own projection.
This is the sole realtime event for Machine Upgrade state changes.

The only local CLI entry point is `multica computer upgrade`. It follows the
Raft service-first rule: a live resident receives the request over its
owner-authenticated loopback control surface; a proven absent resident permits
a locked offline install for the next start; and held resident ownership with
unreachable control fails as `upgrade_service_unreachable` without swapping
the PATH Computer. Offline installation is not successor or convergence proof. Production
and Test continue to select stable/preview packages respectively; only
`--target-version` overrides that environment-owned source.

For a standalone resident, candidate verification is local-first. The target
binds the exclusive control port and proves its exact PID, binary version,
Computer generation, and accepted Workspace binding
set without calling heartbeat or registration. That local proof completes the
handoff. The successor then starts normally and notifies the server that the
upgrade completed. Heartbeat and register claim the new Computer generation
when the successor comes online. There is no predecessor-to-candidate cloud
CAS. Runtime cardinality is not takeover identity.
Rejection before the local proof leaves
the incumbent process valid and does not enter server rollback.

The launcher marks the v2 loopback takeover protocol with the candidate's exact
Computer generation when spawning it, so an inherited environment value cannot
change a later candidate's protocol.
If that marker is absent, the candidate was spawned by a pre-v2 launcher and
projects the legacy `running`/`handoff` health shape from its durable receipt so
the launcher can authorize the same local proof. This is only a local compatibility
view: preflight, Runtime registration, WebSocket connection, and task claims
remain blocked until the CAS commits.

## Deployment order

1. Apply migrations `288_machine_upgrade_contract`,
   `289_machine_upgrade_acceptance`, and
   `290_machine_upgrade_rollback_proof` before deploying a server that
   advertises Machine Upgrade actions. Apply
   `335_daemon_update_detection_contract` before a daemon reports the
   `auto_detect` source or `update_available` outcome. These migrations are
   additive; preserve legacy runtime update rows so already-queued historical
   delivery can recover.
2. Deploy the server. Confirm canonical daemon routes, runtime projection, and
   capability gating are live before any daemon advertises
   `machine_upgrade_v1`.
3. Deploy the capability-bearing daemon/CLI. It detects release availability
   after a two-minute startup delay and every five minutes thereafter, then
   reports the durable target through register/heartbeat. Detection never
   downloads, stages, activates, or restarts; only an explicit machine action
   may mutate the installed release. The old enable/disable setting remains a
   no-op, while the interval flag only adjusts detection cadence.
4. Deploy the web/desktop UI. A runtime is a projection of its daemon's
   canonical operation; sibling status must agree.
5. New callers must use the Computer-scoped machine-upgrade API. Historical
   route and database fields still use `daemon` names. The three
   legacy runtime-scoped HTTP update routes remain temporary compatibility
   adapters: they resolve the runtime's daemon and project or cancel that
   daemon's canonical operation. They must never recreate runtime-owned update
   state.

## Bootstrap limitation

A daemon that predates `machine_upgrade_v1` cannot acquire the new replacement
handoff behavior while it is running. An already supervised service may use
its existing supervisor for the first replacement. An unsupervised legacy
daemon may need one explicit manual restart after a normal out-of-band
installation. Do not report that bootstrap action as converged until a capable
successor registers and all accepted runtimes attest.

## Rollback

Keep the previous PATH binary (`.prev`) and machine-upgrade journal while an
operation is non-terminal. If deployment rollback is necessary, first stop
dispatching new machine actions by withholding the daemon capability, then
roll back server application code while preserving migrations and canonical
rows. Never delete a journal or downgrade schema as a shortcut to claim a
failed takeover was rolled back; a rollback result requires the previous
generation, a distinct restored daemon generation, and the accepted runtime
set to prove live registration. Until then the operation stays
`rollback_pending`, rather than becoming terminal.

## Restart and pre-activation failure recovery

`multica computer restart` preserves the same single-owner boundary as upgrade
handoff: when a resident was observed, its control port must be proven released
before a successor generation is allocated or launched. A graceful shutdown
request without that stop proof is an error, not permission to race another
resident.

The durable journal is a pending-operation marker, not a permanent startup
lock. An `accepted` or `staged` operation has not swapped the PATH Computer.
Once the server acknowledges that exact operation as `failed`, the Computer
clears the matching local marker by operation ID, accepted generation, resolved
target, and accepted Runtime and Workspace sets. If failure reporting was not
acknowledged, or any identity field differs, recovery retains the marker and
fails closed. `handoff`, `candidate_ready`, and `rollback_pending` never use
this pre-activation shortcut because they require successor or rollback proof.

## Superseded recovery markers

The PATH Computer is the live product, same as Raft. A leftover journal whose
source and target both differ from the running binary is superseded by a later
explicit PATH swap. Startup must not fail closed, replay that operation, or
roll back to its source. Keep the old journal for diagnosis.

Recovery still resumes when the running binary *is* the journal source or
target: `accepted`/`staged` continue on the source, successor phases continue
on the target, and `rollback_pending` only restores `.prev` while that same
pair is still the live Computer.
