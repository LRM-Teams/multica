# Research Run V6 contract freeze

Status: design frozen; production disabled.

The normative structural contract is
[`contracts/research-run-v6.schema.json`](contracts/research-run-v6.schema.json),
SHA-256 `f3f2c5f39d8d9490ad84d081f09f8245023dc22ae188935f54b5f393ff58bdcd`.
V6 is an independent protocol, not an extension that may be silently accepted
by V1–V5 decoders. `research-run-v5` remains the default until every E–K exit
condition and the V6 production Adapter pass.

## Four envelopes

- `plan_result` creates a Contract-bound initial Inquiry Graph, execution DAG,
  and Search Plans. Every task names at least one typed Inquiry target.
- `task_result` carries retrieval lineage, screened candidates, explicit
  before/after Inquiry updates, Integration Contributions, derived Insight
  proposals, Dispute proposals, Divergence proposals, report revisions, and
  evaluation. Task kind determines the permitted non-empty fields.
- `prompt_context` is the machine-readable prefix frozen into an Attempt
  manifest. The prose prompt may explain the assignment but cannot add
  canonical entities or grant access absent from this context.
- `gate_input` is a fixed-version view of canonical state. A boolean is a
  server-computed check, not an Agent assertion. Findings and remediation are
  emitted by the Gate implementation, outside this input envelope.

All four envelopes use `additionalProperties: false` at their owned structural
levels. Open JSON objects are explicitly policy-owned payloads (`scope`, cost,
safety, frontier, method, acceptance criteria), and each receives a separately
versioned validator before production enablement.

## Cross-object invariants

JSON Schema validates shape; the Research Run modules and PostgreSQL
transactions enforce the following referential and state rules:

1. Client keys are unique within their kind and resolve only inside the same
   workspace, Run, goal version, and plan version. UUID references must already
   belong to that scope.
2. `confidence_low <= confidence_high`. Branch budget shares are bounded by the
   server-authorized exploration budget; Agent sums do not authorize spend.
3. Inquiry edges resolve both endpoints. `decomposes`, `depends_on`, and
   `refines` remain acyclic. Hypothesis, Branch, and Insight status updates must
   match the stored `before` value and migration-350 transition table.
4. A Query Execution belongs to one accepted Search Plan. A Source Candidate
   can become a Source Snapshot only after an `include` Screening Decision.
   URL, content hash, independence family, mirror relationship, cursor, cost,
   failure, and safety metadata are server-verified.
5. Every accepted Task Result writes an Assimilation Check. A Contribution is
   authored by the original result Agent and names real accepted artifacts.
   Missing or unavailable contributors remain explicit facts.
6. An Insight uses at least two accepted inputs from distinct Tasks or Branches,
   has server-computed level `1 + max(input Insight level)`, and demonstrates
   one declared semantic value. Input hash, scope, and relation form its
   idempotency identity. Stale inputs recursively stale all ancestors.
7. A Dispute has at least two evidence-linked Positions. Deliberation changes
   Position/evidence/scope; prose agreement alone cannot resolve it. Director
   adjudication remains constrained by the active Evidence Standard.
8. Divergence runs in an isolated context and has bounded probe budget.
   Unverified perspectives create only Question, Hypothesis, Branch, or probe
   Task proposals; they cannot create supported Claims or report facts.
9. A report references only fresh accepted Insights and verified Claims. Its
   exact section/Claim/source anchors remain subject to the V5 evidence rules.
   An Evaluation reviews every report Claim, section, and referenced Insight;
   every below-floor dimension has a blocking defect, and a pass has none.
10. Gate checks are recomputed in one current canonical-state view. Passing
    requires resolved or explicitly retained Inquiry, audited Corpus,
    converged Integration, handled material Disputes, bounded Portfolio, fresh
    Insight derivations, current report, independent evaluations, and a current
    pre-delivery Divergence Pass.

## Compatibility and activation

- V1–V5 prompt, result, and gate behavior remains byte/golden frozen.
- The V6 schema hash cannot be updated after the first production V6 Run.
  Semantic changes create `research-run-v7`.
- Adding V6 to `ensureSupportedOrchestratorVersion`, changing
  `OrchestratorVersion`, or accepting schema version 6 is a separate activation
  change and is forbidden until the production decoder, persistence modules,
  recovery, projection, hidden-oracle evaluation, and E–K gates are complete.
