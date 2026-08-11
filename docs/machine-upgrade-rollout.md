# Machine Upgrade rollout and rollback

Machine Upgrade is an additive, daemon-scoped lifecycle. Artifact publication,
staging, and an Active VersionStore pointer are progress only; server
completion requires a capable successor generation plus the complete runtime
set captured at acceptance.

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
5. New callers must use the daemon-scoped machine-upgrade API. The three
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

Keep the previous VersionStore generation and machine-upgrade journal while an
operation is non-terminal. If deployment rollback is necessary, first stop
dispatching new machine actions by withholding the daemon capability, then
roll back server application code while preserving migrations and canonical
rows. Never delete a journal or downgrade schema as a shortcut to claim a
failed takeover was rolled back; a rollback result requires the previous
generation, a distinct restored daemon generation, and the accepted runtime
set to prove live registration. Until then the operation stays
`rollback_pending`, rather than becoming terminal.
