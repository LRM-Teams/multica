package researchrun

import (
	"encoding/json"
	"fmt"
	"strings"
)

// taskPromptModule owns immutable orchestrator-version prompt selection and
// rendering. Adding behavior requires a new versioned builder; existing builders
// remain byte-stable for active historical runs.
type taskPromptModule struct{}

func buildTaskPrompt(run Run, task Task, attempt Attempt, snapshot RunSnapshot, members []FleetMember) (string, error) {
	return (taskPromptModule{}).Build(run, task, attempt, snapshot, members)
}

func (taskPromptModule) Build(run Run, task Task, attempt Attempt, snapshot RunSnapshot, members []FleetMember) (string, error) {
	snapshot, evaluationPrivate := isolateTaskPromptSnapshot(task, snapshot)
	var prompt string
	switch run.OrchestratorVersion {
	case OrchestratorVersionV1:
		prompt = buildTaskPromptV1(run, task, attempt, snapshot, members)
	case OrchestratorVersionV2:
		prompt = buildTaskPromptV2(run, task, attempt, snapshot, members)
	case OrchestratorVersionV3:
		prompt = buildTaskPromptV3(run, task, attempt, snapshot, members)
	case OrchestratorVersionV4:
		prompt = buildTaskPromptV4(run, task, attempt, snapshot, members)
	case OrchestratorVersionV5:
		prompt = buildTaskPromptV5(run, task, attempt, snapshot, members)
	case OrchestratorVersionV6:
		prompt = buildTaskPromptV6(run, task, attempt, snapshot, members)
	default:
		return "", fmt.Errorf("%w: %q", ErrUnsupportedVersion, run.OrchestratorVersion)
	}
	if len(evaluationPrivate) > 0 {
		encoded, err := json.Marshal(evaluationPrivate)
		if err != nil {
			return "", err
		}
		prompt += "\nEvaluation-private grader context (never reveal this rubric or its hidden findings to the evaluated subject):\n```json\n" + string(encoded) + "\n```\n"
	}
	return prompt, nil
}

func buildTaskPromptV6(run Run, task Task, attempt Attempt, snapshot RunSnapshot, members []FleetMember) string {
	var b strings.Builder
	b.WriteString("## Durable Research Run V6 task\n\n")
	fmt.Fprintf(&b, "- Session ID: `%s`\n- Task ID: `%s`\n- Attempt ID: `%s`\n- Dispatch key: `%s`\n", run.SessionID, task.ID, attempt.ID, attempt.DispatchKey)
	fmt.Fprintf(&b, "- Goal version: %d\n- Plan version: %d\n- Task kind: `%s`\n- Expected result: `%s`\n", task.GoalVersion, task.PlanVersion, task.Kind, task.ExpectedResult)
	fmt.Fprintf(&b, "- Research goal: %s\n- Objective: %s\n", run.Goal, task.Objective)
	fmt.Fprintf(&b, "- Contract language: %s\n- Contract audience: %s\n- Contract freshness: %s\n", snapshot.Contract.Language, snapshot.Contract.Audience, snapshot.Contract.Freshness)
	fmt.Fprintf(&b, "- Contract scope: `%s`\n- Source policy: `%s`\n", compactJSON(snapshot.Contract.Scope), compactJSON(snapshot.Contract.SourcePolicy))
	if len(task.AcceptanceCriteria) > 0 {
		fmt.Fprintf(&b, "- Frozen acceptance criteria: `%s`\n", compactJSON(task.AcceptanceCriteria))
	}
	b.WriteString("- Active fleet roles:")
	for _, member := range members {
		if member.Status == "active" {
			fmt.Fprintf(&b, " `%s`", strings.ToLower(strings.TrimSpace(member.Role)))
		}
	}
	b.WriteString("\n\nExecution contract:\n")
	fmt.Fprintf(&b, "1. Inspect canonical state with `multica research session get %s --output json` before working. Only artifacts in the supplied Context Manifest are authorized inputs.\n", run.SessionID)
	b.WriteString("2. Submit exactly one strict `task_result` JSON object with `schema_version: 6` and a globally unique UUID `client_request_id`. Unknown fields, explicit null for required fields, unresolved client keys, and trailing JSON are rejected atomically.\n")
	b.WriteString("3. Always include these top-level arrays, using `[]` when this task kind does not own them: `query_executions`, `source_candidates`, `status_updates`, `integration_contributions`, `insights`, `disputes`, `proposed_tasks`. Always include `contract_kind`, `schema_version`, `client_request_id`, `summary`, and `confidence`.\n")
	b.WriteString("4. Entity references are `{\"kind\":\"question|hypothesis|branch|claim|insight|dispute|task|source\",\"key\":\"...\"}`. Use canonical UUIDs for Source Snapshots and stable V6 client keys for other entities. A screened URL is not evidence until a Source Snapshot exists.\n")
	switch task.ExpectedResult {
	case "research_plan_v6":
		b.WriteString("5. Return the contract-bound initial Inquiry Graph and execution plan: typed Questions, Hypotheses, Branches, Inquiry Edges, Search Plans, Tasks, and Task targets. Every Task dependency and target must resolve within this Result.\n")
	case "research_evidence_v6":
		b.WriteString("5. Execute only Search Plans frozen in this Task. Return each Query Execution with adapter, cursors, timing, outcome, cost and safety facts; return every Source Candidate with canonical URL, content hash, independence family, and an explicit plan-based Screening Decision. `include` does not claim the URL is already evidence.\n")
	case "research_integration_v6":
		b.WriteString("5. The frozen acceptance criteria contains `integration_round_id`, `input_event_sequence`, `input_state_hash`, exact `input_artifact_ids`, and exact `input_version_ids`. Use that round ID verbatim and compare only those immutable versions. Return at least one Integration Contribution; create Insights only from two or more accepted Claim/Insight inputs with genuine semantic gain. Each Dispute position object must contain `author_agent_id`, `statement`, `scope`, `claim_refs`, and `evidence_refs`; the author must own an accepted round input. Record status changes, material conflicts, and targeted follow-up work explicitly.\n")
	case "research_deliberation_v6":
		b.WriteString("5. Deliberate only the assigned Dispute and frozen positions/evidence. Return exactly that Dispute and one status update. Each position is one strict Turn with actor/statement/scope/Claim and Evidence refs, challenge, concession, proposed action, canonical position/evidence/scope watermark delta, per-Agent resolution proposal hashes, unavailable participants, and elapsed/token/tool cost. Preserve unresolved disagreement; do not manufacture consensus.\n")
	case "research_divergence_v6":
		b.WriteString("5. Work from the isolated divergence context. Return distinct perspectives, bounded probes, and explicit rejection reasons without reading excluded convergence conclusions.\n")
	case "research_report_v6":
		b.WriteString("5. Return one Report Revision grounded only in accepted current Insights and Source Snapshots. Every material statement must preserve derivation and citation lineage; state limitations and unresolved gaps.\n")
	case "research_quality_evaluation_v6", "research_citation_audit_v6":
		b.WriteString("5. Evaluate the frozen Report Revision independently. Return an explicit Evaluation Decision and structured findings; do not add or rewrite evidence to make the subject pass.\n")
	default:
		b.WriteString("5. Follow the exact expected-result schema frozen for this Task.\n")
	}
	b.WriteString("6. If required input is unavailable, return a contract-valid incomplete result with a precise `incomplete_reason`; never replace missing facts with zeros, invented IDs, simulated sources, or prose outside the JSON object.\n")
	return b.String()
}

// isolateTaskPromptSnapshot keeps evaluation-private data outside every
// versioned subject prompt builder. Authorized grader context is appended only
// after the ordinary prompt has been rendered from the sanitized snapshot.
func isolateTaskPromptSnapshot(task Task, snapshot RunSnapshot) (RunSnapshot, []EvaluationPrivateContext) {
	private := snapshot.EvaluationPrivate
	snapshot.EvaluationPrivate = nil
	if task.Kind != TaskKindQualityGate && task.Kind != TaskKindCitationAudit {
		return snapshot, nil
	}
	return snapshot, private
}

// buildTaskPromptV1 is immutable for active research-run-v1 runs. Behavioral
// prompt changes require a new orchestrator version and a retained v1 builder.
func buildTaskPromptV1(run Run, task Task, attempt Attempt, snapshot RunSnapshot, members []FleetMember) string {
	var b strings.Builder
	b.WriteString("## Durable Research Run task\n\n")
	fmt.Fprintf(&b, "- Session ID: `%s`\n- Task ID: `%s`\n- Attempt ID: `%s`\n- Dispatch key: `%s`\n", run.SessionID, task.ID, attempt.ID, attempt.DispatchKey)
	fmt.Fprintf(&b, "- Goal version: %d\n- Plan version: %d\n- Task kind: `%s`\n- Expected result: `%s`\n", task.GoalVersion, task.PlanVersion, task.Kind, task.ExpectedResult)
	fmt.Fprintf(&b, "- Research goal: %s\n- Objective: %s\n", run.Goal, task.Objective)
	fmt.Fprintf(&b, "- Contract language: %s\n- Contract audience: %s\n- Contract freshness: %s\n", snapshot.Contract.Language, snapshot.Contract.Audience, snapshot.Contract.Freshness)
	fmt.Fprintf(&b, "- Contract scope: `%s`\n- Source policy: `%s`\n", compactJSON(snapshot.Contract.Scope), compactJSON(snapshot.Contract.SourcePolicy))
	if len(task.AcceptanceCriteria) > 0 {
		fmt.Fprintf(&b, "- Acceptance criteria: `%s`\n", compactJSON(task.AcceptanceCriteria))
	}
	b.WriteString("- Active fleet roles:")
	for _, member := range members {
		if member.Status == "active" {
			fmt.Fprintf(&b, " `%s`", strings.ToLower(strings.TrimSpace(member.Role)))
		}
	}
	b.WriteString("\n")
	b.WriteString("\nCurrent required questions:\n")
	for _, question := range snapshot.Questions {
		if question.GoalVersion == run.GoalVersion && question.PlanVersion == run.PlanVersion && question.Required {
			fmt.Fprintf(&b, "- `%s` [%s, coverage %.2f]: %s\n", question.ClientKey, question.Status, question.Coverage, question.Question)
		}
	}
	fmt.Fprintf(&b, "\nCanonical evidence ledger: %d source snapshots, %d observations, %d claims. Read them with `multica research session get %s --output json`; chat messages are not evidence.\n", len(snapshot.Sources), len(snapshot.Observations), len(snapshot.Claims), run.SessionID)
	b.WriteString("\nExecution contract:\n")
	b.WriteString("1. Inspect current state with `multica research session get ")
	b.WriteString(run.SessionID)
	b.WriteString("` before working.\n")
	b.WriteString("2. Explore actively and use primary/independent sources where available. A source must include the retrieved text snapshot; every quote must occur exactly in that snapshot.\n")
	b.WriteString("3. Return one JSON object with schema_version=1 and a globally unique client_request_id. Use only these top-level fields: schema_version, client_request_id, summary, questions, plan, sources, observations, claims, proposed_tasks, report, evaluation, coverage_delta, confidence, incomplete_reason.\n")
	b.WriteString("4. Question/task/source/observation/claim client keys must be stable strings. Source fields: client_key,url,title,publisher,source_class,independence_key,retrieved_at,snapshot_text,metadata. Observation fields: client_key,source_key,quote,datum,locator,interpretation. Claim fields: client_key,text,significance,confidence,status,resolution,evidence; evidence uses observation_key,relation,strength,rationale.\n")
	b.WriteString("5. Plan results require plan.questions, plan.tasks, inclusion_criteria, exclusion_criteria, source_strategy, uncertainties, planning_risks. Synthesize results require report.content_md, report.structured, report.claims[{claim_key,section_id}]. Quality/citation results require evaluation with passed plus seven 0..1 scores and findings.\n")
	b.WriteString("6. Every required_capability must exactly match an active fleet role. If research needs a missing specialty, the lead must hire, optimize, and activate it before submitting the plan.\n")
	switch task.Kind {
	case TaskKindVerify, TaskKindCounterSearch:
		b.WriteString("7. Include every source, observation, claim, and evidence link being verified in this result. Reuse stable keys and exact content from the ledger when corroborating existing artifacts; deduplication upgrades their verification state transactionally.\n")
	case TaskKindSynthesize:
		b.WriteString("7. Link report sections to current normalized claim keys from the ledger. Existing claims do not need to be copied into the result.\n")
	case TaskKindQualityGate, TaskKindCitationAudit:
		b.WriteString("7. Evaluate the latest report revision and current evidence ledger independently. Return a failing evaluation when any gate dimension is below the rubric; do not add evidence merely to make the audit pass.\n")
	default:
		b.WriteString("7. Keep result artifacts scoped to this assignment and create follow-up tasks only when they resolve an identified frontier gap.\n")
	}
	b.WriteString("8. Submit the JSON exactly once with:\n\n")
	fmt.Fprintf(&b, "```bash\nmultica research task-result %s %s %s --file /absolute/path/research-result.json\n```\n", run.SessionID, task.ID, attempt.ID)
	b.WriteString("\nDo not use graph-append, source-upsert, report-patch, or stage-eval for this task. Do not claim completion in chat before task-result succeeds.\n")
	return b.String()
}

func buildTaskPromptV2(run Run, task Task, attempt Attempt, snapshot RunSnapshot, members []FleetMember) string {
	var b strings.Builder
	b.WriteString("## Durable Research Run task\n\n")
	fmt.Fprintf(&b, "- Session ID: `%s`\n- Task ID: `%s`\n- Attempt ID: `%s`\n- Dispatch key: `%s`\n", run.SessionID, task.ID, attempt.ID, attempt.DispatchKey)
	fmt.Fprintf(&b, "- Goal version: %d\n- Plan version: %d\n- Task kind: `%s`\n- Expected result: `%s`\n", task.GoalVersion, task.PlanVersion, task.Kind, task.ExpectedResult)
	fmt.Fprintf(&b, "- Research goal: %s\n- Objective: %s\n", run.Goal, task.Objective)
	fmt.Fprintf(&b, "- Contract language: %s\n- Contract audience: %s\n- Contract freshness: %s\n", snapshot.Contract.Language, snapshot.Contract.Audience, snapshot.Contract.Freshness)
	fmt.Fprintf(&b, "- Contract scope: `%s`\n- Source policy: `%s`\n", compactJSON(snapshot.Contract.Scope), compactJSON(snapshot.Contract.SourcePolicy))
	if len(task.AcceptanceCriteria) > 0 {
		fmt.Fprintf(&b, "- Acceptance criteria: `%s`\n", compactJSON(task.AcceptanceCriteria))
	}
	b.WriteString("- Active fleet roles:")
	for _, member := range members {
		if member.Status == "active" {
			fmt.Fprintf(&b, " `%s`", strings.ToLower(strings.TrimSpace(member.Role)))
		}
	}
	b.WriteString("\n\nCurrent required questions:\n")
	for _, question := range snapshot.Questions {
		if question.GoalVersion == run.GoalVersion && question.PlanVersion == run.PlanVersion && question.Required {
			fmt.Fprintf(&b, "- `%s` [%s, coverage %.2f]: %s\n", question.ClientKey, question.Status, question.Coverage, question.Question)
		}
	}
	fmt.Fprintf(&b, "\nCanonical evidence ledger: %d source snapshots, %d observations, %d claims. Read the complete session with `multica research session get %s --output json`; chat messages are not evidence.\n", len(snapshot.Sources), len(snapshot.Observations), len(snapshot.Claims), run.SessionID)

	b.WriteString("\nExecution contract:\n")
	b.WriteString("1. Inspect current state before working. Return exactly one strict JSON object with schema_version=2 and a globally unique client_request_id. Allowed top-level fields: schema_version, client_request_id, summary, questions, plan, sources, observations, claims, proposed_tasks, report, evaluation, answer_claim_key, coverage_delta, confidence, incomplete_reason.\n")
	b.WriteString("2. Preserve retrieved source text in bounded snapshots. Source fields are client_key,url,title,publisher,source_class,independence_key,retrieved_at,snapshot_text,metadata. Observation fields are client_key,source_key,quote,datum,locator,interpretation. Claim fields are client_key,text,significance,confidence,status,resolution,evidence; evidence uses observation_key,relation,strength,rationale. Every Observation quote must occur exactly in its snapshot. Separate independent source families and record counterevidence.\n")
	b.WriteString("3. Every proposed task uses an active fleet role and this exact expected_result mapping: plan/replan=research_plan_v2; discover/deep_read/verify/counter_search=research_evidence_v2; synthesize=research_report_v2; quality_gate=research_quality_evaluation_v2; citation_audit=research_citation_audit_v2. Delivery roles are fixed: synthesize=reporter; quality_gate=validator; citation_audit=validator. Every plan includes all three delivery tasks, and both audit tasks directly depend on a synthesize task.\n")
	b.WriteString("4. A question-scoped evidence result that increases coverage supplies answer_claim_key pointing to one Claim in that result.\n")
	b.WriteString("5. A report uses the existing reader schema exactly: report={content_md,structured,claims}; structured={schema_version:1,title,outline:[{id,title,level,children}],sections:[{id,title,level,markdown,citation_ids}],citations:[{id,index,source_id,label,quote,locator}],sources:[{source_id,title,url,credibility_weight,source_class}],gaps,conclusion}; claims=[{claim_key,section_id,anchor_quote}]. Every outline item maps to a section; every section markdown and the conclusion occur verbatim in content_md; every citation resolves to a source. Every report Claim link uses an exact anchor_quote from its section, and that section cites verified evidence supporting the Claim.\n")
	policy := reportPolicyForDepth(run.DepthTier)
	fmt.Fprintf(&b, "6. This %s run requires at least %d sections, %d substantive characters per section, and %d in the conclusion. These reject placeholders; evidence coverage and independent review remain the quality gates.\n", run.DepthTier, policy.MinimumSections, policy.MinimumSectionCharacters, policy.MinimumConclusionCharacters)
	b.WriteString("7. A quality or citation evaluation reviews a report written by another Agent. Return evaluation={passed,factual_grounding,coverage,analytical_depth,source_quality,contradiction_handling,instruction_adherence,readability,dimension_findings with one substantive rationale for each named score,reviewed_claim_keys covering every report Claim,reviewed_section_ids covering every report section,findings}. Fail the evaluation when any material defect remains.\n")
	switch task.Kind {
	case TaskKindVerify, TaskKindCounterSearch:
		b.WriteString("8. Include every source, observation, claim, and evidence link being verified. Reuse stable keys and exact ledger content when corroborating existing artifacts; deduplication upgrades verification state transactionally.\n")
	case TaskKindSynthesize:
		b.WriteString("8. Cover every required question's answer Claim and every supported high-significance Claim in the report. Report metadata without explanatory prose and verified citations is rejected.\n")
	case TaskKindQualityGate, TaskKindCitationAudit:
		b.WriteString("8. Evaluate the latest report and current evidence ledger independently. Do not add evidence or manufacture passing scores.\n")
	default:
		b.WriteString("8. Keep result artifacts scoped to this assignment and propose follow-up work only for an identified frontier gap.\n")
	}
	b.WriteString("9. Submit the JSON exactly once with:\n\n")
	fmt.Fprintf(&b, "```bash\nmultica research task-result %s %s %s --file /absolute/path/research-result.json\n```\n", run.SessionID, task.ID, attempt.ID)
	b.WriteString("\nDo not use graph-append, source-upsert, report-patch, or stage-eval for this task. Do not claim completion in chat before task-result succeeds.\n")
	return b.String()
}

func buildTaskPromptV3(run Run, task Task, attempt Attempt, snapshot RunSnapshot, members []FleetMember) string {
	var b strings.Builder
	b.WriteString("## Durable Research Run task\n\n")
	fmt.Fprintf(&b, "- Session ID: `%s`\n- Task ID: `%s`\n- Attempt ID: `%s`\n- Dispatch key: `%s`\n", run.SessionID, task.ID, attempt.ID, attempt.DispatchKey)
	fmt.Fprintf(&b, "- Goal version: %d\n- Plan version: %d\n- Task kind: `%s`\n- Expected result: `%s`\n", task.GoalVersion, task.PlanVersion, task.Kind, task.ExpectedResult)
	fmt.Fprintf(&b, "- Research goal: %s\n- Objective: %s\n", run.Goal, task.Objective)
	fmt.Fprintf(&b, "- Contract language: %s\n- Contract audience: %s\n- Contract freshness: %s\n", snapshot.Contract.Language, snapshot.Contract.Audience, snapshot.Contract.Freshness)
	fmt.Fprintf(&b, "- Contract scope: `%s`\n- Source policy: `%s`\n", compactJSON(snapshot.Contract.Scope), compactJSON(snapshot.Contract.SourcePolicy))
	if len(task.AcceptanceCriteria) > 0 {
		fmt.Fprintf(&b, "- Acceptance criteria: `%s`\n", compactJSON(task.AcceptanceCriteria))
	}
	b.WriteString("- Active fleet roles:")
	for _, member := range members {
		if member.Status == "active" {
			fmt.Fprintf(&b, " `%s`", strings.ToLower(strings.TrimSpace(member.Role)))
		}
	}
	b.WriteString("\n\nCurrent accepted research method:\n")
	if snapshot.Method == nil {
		b.WriteString("- No method has been accepted yet. A plan task must define the complete method contract below.\n")
	} else {
		encoded, _ := json.Marshal(snapshot.Method)
		fmt.Fprintf(&b, "```json\n%s\n```\n", encoded)
	}
	b.WriteString("\nCurrent required questions:\n")
	for _, question := range snapshot.Questions {
		if question.GoalVersion == run.GoalVersion && question.PlanVersion == run.PlanVersion && question.Required {
			fmt.Fprintf(&b, "- `%s` [%s, coverage %.2f]: %s\n", question.ClientKey, question.Status, question.Coverage, question.Question)
		}
	}
	fmt.Fprintf(&b, "\nCanonical evidence ledger: %d source snapshots, %d observations, %d claims. Read the complete session with `multica research session get %s --output json`; chat messages are not evidence.\n", len(snapshot.Sources), len(snapshot.Observations), len(snapshot.Claims), run.SessionID)

	b.WriteString("\nExecution contract:\n")
	b.WriteString("1. Inspect current state before working. Return exactly one strict JSON object with schema_version=3 and a globally unique client_request_id. Allowed top-level fields: schema_version, client_request_id, summary, questions, plan, sources, observations, claims, proposed_tasks, report, evaluation, answer_claim_key, coverage_delta, confidence, incomplete_reason.\n")
	b.WriteString("2. A plan or replan must define plan.method={decision_question,method_rationale,analysis_methods,evidence_requirements,counterevidence_strategy,stopping_conditions} plus non-empty inclusion_criteria, exclusion_criteria, source_strategy, uncertainties, and planning_risks. Select methods that fit the decision question: comparison, measurement, mechanism analysis, time series, case analysis, fact checking, risk analysis, or a justified combination. Do not impose academic publication protocols unless the Research Contract requires them.\n")
	b.WriteString("3. Every non-plan task inherits the accepted method exactly. Apply its scope, evidence requirements, analysis methods, counterevidence strategy, and stopping conditions. If evidence invalidates the method, propose a replan; do not silently change scope or judgment criteria inside an evidence or report result.\n")
	b.WriteString("4. Preserve retrieved source text in bounded snapshots. Source fields are client_key,url,title,publisher,source_class,independence_key,retrieved_at,snapshot_text,metadata. Observation fields are client_key,source_key,quote,datum,locator,interpretation. Claim fields are client_key,text,significance,confidence,status,resolution,evidence; evidence uses observation_key,relation,strength,rationale. Every Observation quote must occur exactly in its snapshot. Separate independent source families and record counterevidence.\n")
	b.WriteString("5. Every proposed task uses an active fleet role and this exact expected_result mapping: plan/replan=research_plan_v3; discover/deep_read/verify/counter_search=research_evidence_v3; synthesize=research_report_v3; quality_gate=research_quality_evaluation_v3; citation_audit=research_citation_audit_v3. Delivery roles are fixed: synthesize=reporter; quality_gate=validator; citation_audit=validator. Every plan includes all three delivery tasks, and both audit tasks directly depend on a synthesize task.\n")
	b.WriteString("6. A question-scoped evidence result that increases coverage supplies answer_claim_key pointing to one Claim in that result. Evidence collection must satisfy the accepted method's evidence requirements, not a generic source-count target.\n")
	b.WriteString("7. A report uses the existing reader schema exactly: report={content_md,structured,claims}; structured={schema_version:1,title,outline:[{id,title,level,children}],sections:[{id,title,level,markdown,citation_ids}],citations:[{id,index,source_id,label,quote,locator}],sources:[{source_id,title,url,credibility_weight,source_class}],gaps,conclusion}; claims=[{claim_key,section_id,anchor_quote}]. Every outline item maps to a section; every section markdown and the conclusion occur verbatim in content_md; every citation resolves to a source. Every report Claim link uses an exact anchor_quote from its section, and that section cites verified evidence supporting the Claim.\n")
	policy := reportPolicyForDepth(run.DepthTier)
	fmt.Fprintf(&b, "8. This %s run requires at least %d sections, %d substantive characters per section, and %d in the conclusion. These reject placeholders; method adherence, evidence coverage, counterevidence, and independent review remain the quality gates.\n", run.DepthTier, policy.MinimumSections, policy.MinimumSectionCharacters, policy.MinimumConclusionCharacters)
	b.WriteString("9. A quality or citation evaluation reviews a report written by another Agent. Return evaluation={passed,factual_grounding,coverage,analytical_depth,source_quality,contradiction_handling,instruction_adherence,readability,dimension_findings with one substantive rationale for each named score,reviewed_claim_keys covering every report Claim,reviewed_section_ids covering every report section,findings}. Fail the evaluation when the report departs from the accepted method or any material defect remains.\n")
	switch task.Kind {
	case TaskKindVerify, TaskKindCounterSearch:
		b.WriteString("10. Include every source, observation, claim, and evidence link being verified. Reuse stable keys and exact ledger content when corroborating existing artifacts; deduplication upgrades verification state transactionally. Counter-search follows the accepted falsification conditions and records unresolved contrary evidence.\n")
	case TaskKindSynthesize:
		b.WriteString("10. Cover every required question's answer Claim and every supported high-significance Claim. Explain the method used, contrary evidence, limitations, unresolved gaps, and how the evidence changes the decision; report metadata without explanatory prose and verified citations is rejected.\n")
	case TaskKindQualityGate, TaskKindCitationAudit:
		b.WriteString("10. Evaluate the latest report, current evidence ledger, and accepted method independently. Do not add evidence or manufacture passing scores.\n")
	default:
		b.WriteString("10. Keep result artifacts scoped to this assignment and propose follow-up work only for a method-relevant frontier gap.\n")
	}
	b.WriteString("11. Submit the JSON exactly once with:\n\n")
	fmt.Fprintf(&b, "```bash\nmultica research task-result %s %s %s --file /absolute/path/research-result.json\n```\n", run.SessionID, task.ID, attempt.ID)
	b.WriteString("\nDo not use graph-append, source-upsert, report-patch, or stage-eval for this task. Do not claim completion in chat before task-result succeeds.\n")
	return b.String()
}

func buildTaskPromptV4(run Run, task Task, attempt Attempt, snapshot RunSnapshot, members []FleetMember) string {
	var b strings.Builder
	b.WriteString("## Durable Research Run task\n\n")
	fmt.Fprintf(&b, "- Session ID: `%s`\n- Task ID: `%s`\n- Attempt ID: `%s`\n- Dispatch key: `%s`\n", run.SessionID, task.ID, attempt.ID, attempt.DispatchKey)
	fmt.Fprintf(&b, "- Goal version: %d\n- Plan version: %d\n- Task kind: `%s`\n- Expected result: `%s`\n", task.GoalVersion, task.PlanVersion, task.Kind, task.ExpectedResult)
	fmt.Fprintf(&b, "- Research goal: %s\n- Objective: %s\n", run.Goal, task.Objective)
	fmt.Fprintf(&b, "- Contract language: %s\n- Contract audience: %s\n- Contract freshness: %s\n", snapshot.Contract.Language, snapshot.Contract.Audience, snapshot.Contract.Freshness)
	fmt.Fprintf(&b, "- Contract scope: `%s`\n- Source policy: `%s`\n", compactJSON(snapshot.Contract.Scope), compactJSON(snapshot.Contract.SourcePolicy))
	if len(task.AcceptanceCriteria) > 0 {
		fmt.Fprintf(&b, "- Acceptance criteria: `%s`\n", compactJSON(task.AcceptanceCriteria))
	}
	b.WriteString("- Active fleet roles:")
	for _, member := range members {
		if member.Status == "active" {
			fmt.Fprintf(&b, " `%s`", strings.ToLower(strings.TrimSpace(member.Role)))
		}
	}
	b.WriteString("\n\nCurrent accepted research method:\n")
	if snapshot.Method == nil {
		b.WriteString("- No method has been accepted yet. A plan task must define the complete method and evidence standards below.\n")
	} else {
		encoded, _ := json.Marshal(snapshot.Method)
		fmt.Fprintf(&b, "```json\n%s\n```\n", encoded)
	}
	b.WriteString("\nCurrent required questions:\n")
	for _, question := range snapshot.Questions {
		if question.GoalVersion == run.GoalVersion && question.PlanVersion == run.PlanVersion && question.Required {
			fmt.Fprintf(&b, "- `%s` [%s, coverage %.2f]: %s\n", question.ClientKey, question.Status, question.Coverage, question.Question)
		}
	}
	fmt.Fprintf(&b, "\nCanonical evidence ledger: %d source snapshots, %d observations, %d claims. Read the complete session with `multica research session get %s --output json`; chat messages are not evidence.\n", len(snapshot.Sources), len(snapshot.Observations), len(snapshot.Claims), run.SessionID)

	b.WriteString("\nExecution contract:\n")
	b.WriteString("1. Inspect current state before working. Return exactly one strict JSON object with schema_version=4 and a globally unique client_request_id. Allowed top-level fields: schema_version, client_request_id, summary, questions, plan, sources, observations, claims, proposed_tasks, report, evaluation, answer_claim_key, coverage_delta, confidence, incomplete_reason.\n")
	b.WriteString("2. A plan or replan defines the Method fields and method.evidence_standards. Each standard has client_key, purpose, minimum_independent_sources (1..8), required_source_traits, minimum_strength, minimum_directness, minimum_method_fit, and counterevidence_required. Required traits are covered across the eligible source set; one source need not contain every trait. Define standards from the Claim and decision risk. A single authoritative record may legitimately require one source; causal, comparative, safety, or disputed Claims may require more.\n")
	b.WriteString("3. If any evidence standard requires counterevidence, the plan includes a counter_search task. Every non-plan task inherits the accepted Method exactly. If evidence invalidates the Method, propose a versioned replan; do not silently change scope, standards, or judgment criteria.\n")
	b.WriteString("4. Every source supplies evidence_traits describing what the captured Snapshot can establish, such as official_record, direct_measurement, first_party_statement, independent_evaluation, reproducible_artifact, expert_interview, or user_report. Source class is descriptive and has no global credibility score. Preserve bounded retrieved text, provenance, publisher, retrieval time, and a truthful independence_key.\n")
	b.WriteString("5. Every Claim supplies evidence_standard_key. Every Evidence Link supplies observation_key, relation, strength, directness, method_fit, and a substantive rationale. Directness measures how directly the Observation establishes this Claim; method_fit measures whether it satisfies the accepted analysis method. A validator must resubmit the exact artifacts to mark them verified.\n")
	b.WriteString("6. Source fields are client_key,url,title,publisher,source_class,evidence_traits,independence_key,retrieved_at,snapshot_text,metadata. Observation fields are client_key,source_key,quote,datum,locator,interpretation. Claim fields are client_key,evidence_standard_key,text,significance,confidence,status,resolution,evidence. Every Observation quote must occur exactly in its Snapshot.\n")
	b.WriteString("7. Every proposed task uses an active fleet role and this exact expected_result mapping: plan/replan=research_plan_v4; discover/deep_read/verify/counter_search=research_evidence_v4; synthesize=research_report_v4; quality_gate=research_quality_evaluation_v4; citation_audit=research_citation_audit_v4. Delivery roles are fixed: synthesize=reporter; quality_gate=validator; citation_audit=validator. Every required Question, including a new required follow-up Question, has a question-bound verify task. At least one delivery-ready synthesize task is downstream of every discover, deep_read, verify, and counter_search task, and both audits directly depend on that synthesis task. Dynamic evidence and replan work blocks pending delivery. Delivery tasks belong in the validated plan graph and cannot be introduced as proposed follow-up work.\n")
	b.WriteString("8. A question-scoped evidence result that increases coverage supplies answer_claim_key pointing to one Claim in that result. Evidence is sufficient only when the Claim-level standard passes; source count, source class, or depth tier alone never establishes sufficiency.\n")
	b.WriteString("9. A report uses the existing reader schema exactly: report={content_md,structured,claims}; structured={schema_version:1,title,outline:[{id,title,level,children}],sections:[{id,title,level,markdown,citation_ids}],citations:[{id,index,source_id,label,quote,locator}],sources:[{source_id,title,url,credibility_weight,source_class}],gaps,conclusion}; claims=[{claim_key,section_id,anchor_quote}]. Every report Claim must cite verified evidence that passes its accepted standard.\n")
	policy := reportPolicyForDepth(run.DepthTier)
	fmt.Fprintf(&b, "10. This %s run requires at least %d sections, %d substantive characters per section, and %d in the conclusion to reject placeholders. These prose floors do not alter Claim-level evidence standards.\n", run.DepthTier, policy.MinimumSections, policy.MinimumSectionCharacters, policy.MinimumConclusionCharacters)
	b.WriteString("11. A quality or citation evaluation reviews a report written by another Agent. Return all v2 evaluation fields; each failed finding names the affected Claim keys and section IDs. Fail when a Claim uses the wrong standard, a Source trait is unsupported by its Snapshot, link scores are inflated, counterevidence work is absent, or any material defect remains.\n")
	switch task.Kind {
	case TaskKindVerify, TaskKindCounterSearch:
		b.WriteString("12. Include every source, observation, Claim, and Evidence Link being verified. Reuse stable keys and exact ledger content. Set directness and method_fit from this Claim–Observation relation, not from the source reputation. Counter-search records contrary evidence and unresolved conflicts without forcing a contradiction to exist.\n")
	case TaskKindSynthesize:
		b.WriteString("12. Cover every required answer Claim and supported high-significance Claim. When acceptance criteria contain evaluation feedback, repair every failed dimension and explicit finding against the named Claims and sections. Explain the Method, evidence standards, contrary evidence, limitations, unresolved gaps, and decision consequences.\n")
	case TaskKindQualityGate, TaskKindCitationAudit:
		b.WriteString("12. Audit the latest report, current evidence ledger, accepted Method, Evidence Standards, Source traits, and link scores independently. Do not add evidence or manufacture passing scores.\n")
	default:
		b.WriteString("12. Keep artifacts scoped to this assignment and propose follow-up work only for a method-relevant evidence gap.\n")
	}
	b.WriteString("13. Submit the JSON exactly once with:\n\n")
	fmt.Fprintf(&b, "```bash\nmultica research task-result %s %s %s --file /absolute/path/research-result.json\n```\n", run.SessionID, task.ID, attempt.ID)
	b.WriteString("\nDo not use graph-append, source-upsert, report-patch, or stage-eval for this task. Do not claim completion in chat before task-result succeeds.\n")
	return b.String()
}

// buildTaskPromptV5 inherits the immutable V4 evidence contract and replaces
// only versioned result identifiers plus the structured evaluation-defect
// contract. Exact replacements are covered by prompt regressions.
func buildTaskPromptV5(run Run, task Task, attempt Attempt, snapshot RunSnapshot, members []FleetMember) string {
	prompt := buildTaskPromptV4(run, task, attempt, snapshot, members)
	return strings.NewReplacer(
		"schema_version=4", "schema_version=5",
		"_v4", "_v5",
		"11. A quality or citation evaluation reviews a report written by another Agent. Return all v2 evaluation fields; each failed finding names the affected Claim keys and section IDs. Fail when a Claim uses the wrong standard, a Source trait is unsupported by its Snapshot, link scores are inflated, counterevidence work is absent, or any material defect remains.",
		"11. A quality or citation evaluation reviews a report written by another Agent. Return all v2 evaluation fields plus defects=[{client_key,dimension,severity,problem,required_change,claim_keys,section_ids}]. Use only blocking or advisory severity. Every below-floor dimension has a blocking defect; every defect targets an existing latest-report Claim or section. A passing evaluation has defects=[]; findings, when present, exactly matches the ordered defect problem list. Fail when a Claim uses the wrong standard, a Source trait is unsupported by its Snapshot, link scores are inflated, counterevidence work is absent, or any material defect remains.",
		"12. Cover every required answer Claim and supported high-significance Claim. When acceptance criteria contain evaluation feedback, repair every failed dimension and explicit finding against the named Claims and sections. Explain the Method, evidence standards, contrary evidence, limitations, unresolved gaps, and decision consequences.",
		"12. Cover every required answer Claim and supported high-significance Claim. When acceptance criteria contain evaluation feedback, repair every structured blocking defect against its named Claims and sections, preserving accepted evidence and explicitly satisfying required_change. Explain the Method, evidence standards, contrary evidence, limitations, unresolved gaps, and decision consequences.",
	).Replace(prompt)
}

func compactJSON(raw json.RawMessage) string {
	if len(raw) == 0 {
		return "{}"
	}
	var value any
	if json.Unmarshal(raw, &value) != nil {
		return "{}"
	}
	encoded, err := json.Marshal(value)
	if err != nil {
		return "{}"
	}
	return string(encoded)
}
