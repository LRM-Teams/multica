---
status: accepted
---

# Local Machine Upgrade proof completes without cloud generation CAS

Raft Computer finishes a replacement when the successor proves itself
locally: loopback `machine-attestation`, live PID, matching version, and the
old service gone. The cloud is not a predecessor-to-candidate CAS. After that
local proof the new Computer just comes online (`ready` / heartbeat).

Multica's detached handoff used a second cloud transaction,
`POST /computer/machine-upgrades/{id}/takeover`, that required
`candidate == predecessor + 1` and mutated `computer_generation` before the
successor could start. A 400 there failed an already-spawned successor
(`complete takeover identity is required`) even when local PID+version proof
was good.

The local incumbent/candidate loopback proof stays. That is the completion
condition. Heartbeat and register already claim a newer Computer generation
when the successor comes online. Attest notifies the server that the upgrade
completed. The `/takeover` HTTP route remains only as a compatibility receipt
for older Computers that still POST it; it does not CAS generation.

Cloud generation CAS is not upgrade completion, and it is not Raft alignment.
