# Research structured-evaluation-defect implementation record

Date: 2026-08-05

Branch: `feat/research-structured-evaluation-defects`

Target: make every failed report evaluation produce addressable, report-bound
defects instead of relying on free-text reviewer findings.

## Diagnosis

- [x] V4 validates score rationales and full reviewed Claim/section sets, but
  `findings` remains a list of untyped strings.
- [x] The server cannot prove which Claim or section a finding targets, whether
  all referenced targets exist in the reviewed report, or whether every
  below-floor dimension has a blocking repair instruction.
- [x] This is user-reachable: a report revision can receive a detailed paragraph
  yet still guess what artifact to change; the next review may repeat the same
  defect under different wording.
- [x] Changing V4 in place would violate stored orchestrator replay semantics.
  The new contract therefore requires a V5 result/prompt version while V1–V4
  remain accepted and immutable.

## Contract

- A V5 evaluation emits stable defects with a client key, evaluated dimension,
  severity, problem statement, required change, target Claim keys, and target
  section IDs.
- Failed evaluations require at least one blocking defect. Every dimension below
  the run's score floor requires a blocking defect in that dimension.
- Passing evaluations contain no defects; advisory defects may accompany a
  failed review but cannot disappear into an otherwise passing hidden Decision.
- Every target key must exist in the latest reviewed report. A defect must target
  at least one Claim or section; global prose without an address is invalid.
- Acceptance and projection share the same bounds: at most 16 defects, 64 Claim
  keys and 64 section IDs per defect, and 1024 bytes each for problem and
  required change. A valid V5 Decision cannot be silently truncated on its way
  into remediation.
- Legacy `findings` is derived from the structured defect summaries for durable
  compatibility. If the Agent submits both, the two ordered summary lists must
  match exactly so there is one fact, not two conflicting versions.
- Evaluation feedback projection carries bounded structured defects into the
  existing remediation objective and task acceptance criteria.

## Checklist

- [x] Add immutable V5 dispatch/result contracts and keep V1–V4 behavior pinned.
- [x] Validate structured defects and report target membership.
- [x] Persist and project defects through Decision → Gate → revision task.
- [x] Extend backend metrics and built-in skill/source documentation.
- [x] Add unit and PostgreSQL regressions, run adjacent suites, and record results.

## Implementation

- New runs use `research-run-v5`; result dispatch, expected-result identifiers,
  acceptance schema, task prompts, version validation, evidence-fitness behavior,
  source projection, and metrics all recognize V5.
- V5 layers only the evaluation-defect contract over V4. It translates a copy
  into the immutable V4 validator so Method, evidence, graph, report, and audit
  rules remain shared without changing V4 input behavior.
- V1–V4 reject `evaluation.defects`. V5 rejects unknown fields through the same
  strict decoder, normalizes legacy findings from defects, and persists that
  normalized evaluation in the Decision outcome.
- PostgreSQL validation joins defect Claim/section targets to the latest report
  before accepting the result. It applies the same depth score floor as the
  delivery Gate and requires a blocking defect for every below-floor dimension.
- Failed Gate projection carries all valid V5 defects into both the synthesis
  objective and `acceptance_criteria.remediation.target_findings`. A fresh
  independent evaluation of the new report remains the completion test.

## Bugs found while implementing

- Switching the default orchestrator exposed eight PostgreSQL integration
  fixtures that initialized a current run but still submitted V4 envelopes.
  Only those current-run fixtures were upgraded to V5; explicit V4 compatibility
  tests remain unchanged.
- The first bounds design accepted up to 256 defects with 12KB text but projected
  only 16 defects with 1KB text. That would silently drop valid repair work.
  V5 acceptance now uses the projection limits, and regressions reject 17th
  defects, 65th per-defect targets, and text beyond 1024 bytes.
- Passing evaluations initially allowed advisory defects. Because the current
  Run Snapshot does not expose Decision advisories, that would hide findings
  from the user. V5 now requires `defects=[]` on a pass; advisory defects can
  accompany a failed review and remain visible in its remediation record.

## Frontend coordination

- `packages/views/research/components/research-node-detail.tsx` should map the
  five `research_*_v5` expected-result identifiers to the existing localized
  plan/evidence/report/evaluation labels. Until then the detail view shows the
  raw V5 identifier; execution and persistence are unaffected.
- To present repair instructions instead of raw JSON, the node detail should
  render `acceptance_criteria.remediation.target_findings[].metadata.defects`
  as defect key, dimension, problem, required change, Claim keys, and section
  IDs. No new backend endpoint is required.

## Verification

- [x] V5 structure, legacy rejection, score-floor/target validation, prompt,
  projection bounds, PostgreSQL persistence, invalid-target rejection, and
  remediation path:
  `go test ./internal/researchrun -run 'TestV5Evaluation|TestV4RejectsV5|TestTaskPromptV5|TestV5EvaluationDefectsPersist|TestEvaluationFeedbackMetadata' -count=1`
- [x] Full research-run package with isolated PostgreSQL:
  `go test ./internal/researchrun -count=1`
- [x] Adjacent handler and metrics packages with isolated PostgreSQL:
  `go test ./internal/handler ./internal/metrics -count=1`
- [x] Built-in research skill/service tests:
  `go test ./internal/service -run 'Research|BuiltinSkill' -count=1`
- [x] Metrics and service focused rerun:
  `go test ./internal/metrics ./internal/service -run 'Research|BuiltinSkill|BusinessSampler' -count=1`
- [x] Static analysis:
  `go vet ./internal/researchrun ./internal/handler ./internal/metrics`
- [x] Whitespace validation: `git diff --check`
