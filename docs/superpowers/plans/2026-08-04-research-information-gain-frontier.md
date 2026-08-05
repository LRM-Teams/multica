# Research information-gain and frontier implementation record

Started: 2026-08-04

Last updated: 2026-08-05

Branch: `feat/research-information-gain-frontier`

Target: make Observe → Evaluate use server-observed evidence-graph changes and a ranked unresolved-question frontier, rather than row-count heuristics and Agent-declared priority alone.

## Completed diagnosis

- [x] Inspected Result acceptance, Question materialization/progress, Run Stats, marginal-gain Gate, task activation, and Gate question selection.
- [x] Confirmed that the documented “recalculate Research Frontier” step does not exist. Questions retain Agent-supplied priority/impact/uncertainty/novelty, and Gate orders only by priority then impact/uncertainty.
- [x] Confirmed that `measuredInformationGain` is `new_sources*0.02 + new_observations*0.01 + new_claims*0.02`. It ignores verified upgrades, new Evidence Links, counterevidence, conflict resolution, Question answers, and diminishing novelty as the graph grows.
- [x] Confirmed that a verifier upgrading existing pending artifacts can produce measured gain `0`, while repeated new low-value rows can keep gain above the stopping threshold.
- [x] Found a related user-reachable routing defect: an “answered” Question with only pending/unverified support is reported as unanswered, then routed back to discovery instead of verification because the finding does not explain why it is incomplete.

## Design boundary

- Information gain is derived from canonical graph state before and after accepted evidence work. The Agent's `coverage_delta` remains a bounded proposal used for Question progress, not the stopping metric by itself.
- Gain components distinguish verified required-answer coverage, answered transitions, independent verified source families, verified Evidence Links, verified contradictions, Claim resolutions/adjudication, and diminishing raw graph novelty.
- Every evidence-batch gain evaluation is persisted as a Decision with before/after state, canonical-change flag, component scores, total score, and low-gain result.
- Frontier selection uses a deterministic score over priority, impact, uncertainty, novelty, missing coverage, and contradiction/gap kind. The selected Question ID and score are exposed in Gate metadata and bound to remediation work.
- A Question with an answer Claim but missing verified support routes to `verify`; a Question without an answer routes to `discover`.
- Existing result schemas remain pinned. This target changes server observation and routing, not the Agent result envelope.

## Implementation checklist

- [x] Add canonical graph-state snapshots and componentized measured gain.
- [x] Persist information-gain Decisions and event detail atomically with result acceptance.
- [x] Replace raw-row-only low-gain streak updates with the server-derived score.
- [x] Rank and expose the highest-value unresolved required Question.
- [x] Route evidence-backed-but-unverified Questions to verification.
- [x] Add unit and PostgreSQL regressions for duplicate batches, verification upgrades, counterevidence/resolution, diminishing novelty, frontier ranking, and route selection.
- [x] Update runtime contracts, built-in skill, source map, and this record.
- [x] Run focused worktree PostgreSQL tests and broader backend checks.

## Bugs encountered

- V4 accepted delivery plans where synthesis did not transitively depend on every evidence task. That allowed pending discovery to reach a report that could not persist, and allowed parallel deep-read work to finish after a report that omitted it. V4 validation now requires a question-bound verify task for every required Question, a delivery synthesis transitively downstream of every discover/deep-read/verify/counter-search, and both audits directly downstream of that synthesis.
- V4 also allowed an evidence result to inject a new synthesis/audit through `proposed_tasks`, bypassing the validated delivery DAG. Delivery follow-ups are now rejected outside `plan.tasks`; evidence results may still propose bounded evidence work or a method-invalidating replan.
- Non-evidence results overwrote `run_stats.last_measured_gain` with zero even though they did not participate in the low-gain streak. Stats now retain the last actual evidence measurement.
- The first graph-state query counted every historical session source, including superseded goal/plan evidence. It now derives current sources and observations through current-plan Claim Evidence Links; reused evidence counts only when linked into the current graph.
- Verify/counter-search could attach verified Evidence Links to an existing Claim but could not change a non-proposed Claim's status, lower its confidence, or revise its resolution. Verified adjudication now replaces those fields while discovery/deep-read work retains monotonic proposal behavior.
- A discovery Agent could obtain resolution gain by writing resolution prose onto a Claim whose evidence was still pending. Resolution gain now requires the Claim to participate in the canonical verified-evidence graph; the PostgreSQL regression proves the same stable Claim scores resolution only after verification adjudicates it.
- V4 validated only the initial plan graph. Evidence tasks could add discovery/deep-read/verify follow-ups that were not dependencies of delivery synthesis, so a report could run before proactive exploration completed. Dynamic evidence tasks now depend on their producing task and become dependencies of pending delivery synthesis; Gate also rejects a report when a later Information Gain Decision records a real canonical graph change, forcing a report revision for late control work or pre-fix runs.
- Dynamic required Questions previously did not inherit V4's verification-path invariant, and an evidence result could propose replan while the old delivery became ready. V4 now rejects required follow-up Questions without question-bound verify work and treats proposed replan as a delivery-blocking dependency, so invalidated methods cannot race their old report.
- Report freshness initially compared report time with every succeeded evidence task. That would force a pointless rewrite after a duplicate zero-change saturation probe, while row counts could miss a verified Claim adjudication that changed status, confidence, or resolution. Information-gain Decisions now record `canonical_changed`, graph state includes a deterministic Claim-adjudication fingerprint, verified adjudication has its own gain component, and report freshness responds only to actual canonical graph changes.
- The first verified-coverage component multiplied Agent-proposed `question.coverage`, so a verifier could amplify gain by choosing a large delta. Verified coverage is now the server-derived fraction of required Questions whose answer Claim has verified support; the separate answer transition requires that verified Claim to reach a terminal adjudicated status. `coverage_delta` no longer enters the information-gain score.
- Focused handler validation selected a broader playbook test and exposed case/heading-sensitive assertions even though the required instruction and failure-test content were present. This was test-only, not a user-reachable runtime defect; the assertions now normalize case/whitespace and test the semantic marker instead of one heading layout.

## Verification log

- `TEST_DATABASE_URL="$DATABASE_URL" go test ./internal/researchrun -run 'TestMeasuredInformationGain|TestInformationGainTracksCanonicalVerificationUpgrade|TestPostgresStoreRunsFromPlanThroughConfirmedDelivery|TestResearchRunV4|TestRemediation' -count=1` — passed against the isolated worktree PostgreSQL database after the final graph/delivery changes.
- `TEST_DATABASE_URL="$DATABASE_URL" go test ./internal/researchrun -count=1` — final full package run passed after server-derived coverage and Claim-adjudication fingerprinting.
- `TEST_DATABASE_URL="$DATABASE_URL" go test ./internal/researchrun ./internal/handler ./internal/metrics -count=1` — passed against the isolated worktree PostgreSQL database.
- `TEST_DATABASE_URL="$DATABASE_URL" go test ./internal/service -run 'Research|BuiltinSkill' -count=1` — passed.
- `go vet ./internal/researchrun ./internal/handler ./internal/metrics` — passed.
- `git diff --check` — passed.
