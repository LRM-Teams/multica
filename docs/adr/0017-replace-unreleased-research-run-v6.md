---
status: accepted
---

# Replace the unreleased Research Run V6 contract in place

Date: 2026-08-14

## Context

The previously frozen Research Run V6 contract has never been accepted by a
production decoder and no production Run uses it. Its fixed Plan/Task/Report and
review-role model conflicts with the accepted Ronaldo Director design: one
user-selected Director, dynamic Agents, tiered absorption, persistent Discussion,
bounded Director Briefs and sandboxed HTML Reports.

Using V7 for the accepted design would keep two incompatible names for behavior
that has never had a production compatibility boundary. Reusing V6 was safe
because the in-place replacement completed while production still rejected V6
and V1–V5 remained byte/behavior compatible.

## Decision

Replace the unreleased V6 contract in place under `research-run-v6`. The
normative product specification is
[`../superpowers/specs/2026-08-14-ronaldo-research-director-development-spec.zh-CN.md`](../superpowers/specs/2026-08-14-ronaldo-research-director-development-spec.zh-CN.md).
The target machine and storage contracts are
[`../research-run-v6-contract.md`](../research-run-v6-contract.md) and
[`../research-run-v6-storage-contract.md`](../research-run-v6-storage-contract.md);
the target transport contract is
[`../research-run-v6-http-contract.md`](../research-run-v6-http-contract.md).

The code-coupled old Schema is replaced atomically in implementation Slice 0.
Omitting `orchestrator_version` continues to default to V5. An explicit
`research-run-v6` request with a Director creates V6, while the workspace V6
release control can close new creates and pause existing Runs. Activation
evidence audits readiness to change the omitted-version default; it does not
disable explicit V6 create. V1–V5 Schema, Prompt hashes, decoders, historical
rows and readers must not change.

## Consequences

- Old V6 code and documents are historical evidence, not implementation input.
- No compatibility decoder or migration is required between the two unshipped V6
  designs.
- The frozen Ronaldo V6 Schema hash is the production compatibility boundary.
  A later incompatible change requires a new protocol version.
- Contract replacement, decoder/golden updates, builtin Skill/source map and
  activation evidence must ship in the ordered implementation series; the Skill
  is not updated before the behavior exists.
