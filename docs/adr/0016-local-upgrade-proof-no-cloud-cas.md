---
status: accepted
---

# Machine Upgrade successor completion has no cloud generation CAS

Raft Computer treats the newly started Binding socket as successor proof. The
cloud is not a predecessor-to-candidate generation CAS and Runtime cardinality
is not takeover identity.

Multica's detached handoff used a second cloud transaction,
`POST /computer/machine-upgrades/{id}/takeover`, that required
`candidate == predecessor + 1` and mutated a numeric Computer tenure before the
successor could start. A 400 there failed an already-spawned successor
(`complete takeover identity is required`) even when local PID+version proof
was good.

Multica now follows that boundary directly. Before swapping the PATH binary,
the execution child writes a Raft-shaped marker containing the request and
versions plus `sourceServicePid`, old managed-runner PIDs, the accepted managed
Workspace set/revision, `observedTargetGeneration`, and `targetServicePid`.
The successor answers `machine-attestation` on the existing framed local IPC
surface. Its version, PID, source PID, non-empty `serviceGeneration`, and full
managed set must match before the target identity is observed. The journal is
removed only after the predecessor service and old managed runners are dead.
If the target process changes, the next valid successor replaces the observed
target identity instead of waiting forever on the stale process. There is no
loopback HTTP attestation adapter or cloud generation CAS.

Authorization is separate from binary execution. A Web request authorizes the
Computer owner and sends `computer:upgrade` over one current Binding socket; it
does not create a server operation row. The local `multica computer upgrade`
command uses its saved human session for that same request before delivering it
through authenticated loopback. The request ID correlates command, progress,
successor completion, and UI; it is not a cloud operation identity.

Cloud generation CAS and cloud receipt tables are not upgrade completion, and
restoring them would not be Raft alignment.
