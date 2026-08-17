# Machine Upgrade rollout and rollback

Machine Upgrade is a Computer-scoped, Raft-style successor reconciliation.
The server authorizes the Computer owner and sends `computer:upgrade` through
one current Workspace Binding socket. It does not create a cloud operation or
receipt row. The caller-provided `requestId` correlates progress, successor
completion, and the UI.

The selected Binding child asks Computer Host to prepare every current child,
then downloads and verifies the release. Before replacing the on-PATH binary it
writes the permission-restricted pending marker:

```json
{
  "requestId": "...",
  "fromVersion": "v1.0.0",
  "targetVersion": "v1.1.0",
  "startedAt": "...",
  "schemaVersion": 1
}
```

After the swap, the incumbent exits. The successor reconstructs its desired
Bindings and waits for a real current Binding socket. It then sends the original
`requestId` as `computer:upgrade:done`:

- running version equals `targetVersion`: `ok=true`;
- running version differs: `ok=false`, `rolledBack=true`, and `newVersion` is
  the running version.

The successor removes the marker after the Binding socket accepts the frame. If
there is no ready Binding or the frame write fails, startup fails and the marker
stays for retry on the next start. Web version convergence is only a fallback
for a transient completion event lost after a successful socket write; it is
not a second upgrade state machine.

There is deliberately no accepted generation, accepted Runtime/Workspace set,
HTTP progress/attestation, takeover CAS, or cloud receipt table. Those belonged
to the retired pre-Raft flow and must not be restored.

## Compatibility boundary

The current reader accepts the immediately previous `target_version` marker so
`v0.4.24-alpha.81` can upgrade directly. That marker has no request ID, so a
matching successor can only consume it; it cannot reconstruct a correlated done
event. Remove this reader when that release is no longer a supported direct
upgrade source, as required by the adjacent code TODO.

## Pre-activation failures and rollback

Download or verification failure leaves the installed binary untouched and
emits a correlated failed done event. A swap failure removes the marker because
no successor handoff occurred. Once a marker exists across restart, a successor
whose running version differs from its target reports an explicit rollback
result and consumes the marker only after delivery.

The previous PATH binary (`.prev`) remains the executable rollback material.
Never delete the pending marker merely to claim rollback or completion.

## Offline CLI upgrade

`multica computer upgrade` first probes the machine-wide resident. A live owner
receives the request through owner-authenticated loopback. Only a proven absent
resident permits a locked offline install for the next start; held resident
ownership with unavailable control returns `upgrade_service_unreachable` and
never swaps PATH. Offline installation is not successor or completion proof.
