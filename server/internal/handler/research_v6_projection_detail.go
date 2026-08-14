package handler

import (
	"fmt"
	"sort"
	"strings"
)

const (
	researchV6DetailNotApplicable = "not_applicable"
	researchV6DetailNotRecorded   = "not_recorded"
)

var researchV6CanonicalDetailFields = []string{
	"purpose", "objective", "entry_condition", "method", "input_artifacts",
	"actions_taken", "actor", "result", "evidence", "decision", "failure",
	"recovery", "upstream", "downstream",
}

// canonicalResearchV6Detail adds the stable A5 detail contract to a real
// run-v2 projection payload. Values come from canonical ledger fields; the
// bounded semantic labels describe entity purpose and never infer facts from
// node title or summary prose.
func canonicalResearchV6Detail(kind, status string, actor *string, payload map[string]any) map[string]any {
	detail := make(map[string]any, len(payload)+len(researchV6CanonicalDetailFields))
	for key, value := range payload {
		detail[key] = value
	}
	for _, field := range researchV6CanonicalDetailFields {
		detail[field] = researchV6DetailNotApplicable
	}
	detail["actor"] = researchV6ActorDetail(actor, payload)
	detail["upstream"] = researchV6DetailNotRecorded
	detail["downstream"] = researchV6DetailNotRecorded

	switch kind {
	case runGraphKindRoot:
		detail["purpose"] = "govern_research_run"
		detail["objective"] = researchV6NestedValue(payload, "content", "goal")
		detail["entry_condition"] = map[string]any{"run_status": payload["run_status"], "goal_version": payload["goal_version"]}
		detail["method"] = researchV6ValueOr(payload["method"], map[string]any{"phase": payload["phase"]})
		detail["input_artifacts"] = map[string]any{"contract": payload["contract"], "method": payload["method"]}
		detail["actions_taken"] = map[string]any{"state_version": payload["state_version"], "plan_version": payload["plan_version"]}
		detail["result"] = map[string]any{"status": status, "run_stats": payload["run_stats"], "stop_reason": payload["stop_reason"], "last_error": payload["last_error"]}
		detail["decision"] = map[string]any{"status": payload["run_status"], "phase": payload["phase"]}
		detail["failure"] = researchV6RunFailure(payload)
		detail["recovery"] = map[string]any{"next_reconcile_at": payload["next_reconcile_at"], "run_config": payload["run_config"]}
	case runGraphKindQuestion:
		detail["purpose"] = "resolve_research_question"
		detail["objective"] = researchV6NestedValue(payload, "content", "goal")
		detail["entry_condition"] = map[string]any{"parent_question_id": payload["parent_question_id"], "created_by_task_id": payload["created_by_task_id"], "required": payload["required"]}
		detail["method"] = map[string]any{"question_kind": payload["question_kind"], "priority": payload["priority"]}
		detail["input_artifacts"] = researchV6CompactReferences(payload, "parent_question_id", "created_by_task_id")
		detail["actions_taken"] = map[string]any{"question_status": payload["question_status"]}
		detail["result"] = map[string]any{"answer_claim_id": payload["answer_claim_id"], "terminal_explanation": payload["terminal_explanation"]}
		detail["evidence"] = researchV6ValueOr(payload["answer_claim_id"], researchV6DetailNotRecorded)
		detail["decision"] = payload["question_status"]
		detail["failure"] = researchV6QuestionFailure(payload)
		detail["recovery"] = researchV6QuestionRecovery(payload)
	case runGraphKindTask:
		detail["purpose"] = "execute_research_task"
		detail["objective"] = researchV6NestedValue(payload, "content", "goal")
		detail["entry_condition"] = map[string]any{"question_id": payload["question_id"], "parent_task_id": payload["parent_task_id"], "ready_at": payload["ready_at"]}
		detail["method"] = map[string]any{"task_kind": payload["task_kind"], "required_capability": payload["required_capability"], "acceptance_criteria": payload["acceptance_criteria"]}
		detail["input_artifacts"] = researchV6CompactReferences(payload, "question_id", "parent_task_id")
		detail["actions_taken"] = map[string]any{"attempt_count": payload["attempt_count"], "started_at": payload["started_at"], "completed_at": payload["completed_at"]}
		detail["result"] = map[string]any{"status": payload["task_status"], "expected_result": payload["expected_result"], "completed_at": payload["completed_at"]}
		detail["decision"] = payload["task_status"]
		detail["failure"] = researchV6TaskFailure(payload)
		detail["recovery"] = map[string]any{"attempt_count": payload["attempt_count"], "max_attempts": payload["max_attempts"], "terminal_reason": payload["terminal_reason"]}
	case runGraphKindAttempt:
		detail["purpose"] = "execute_task_attempt"
		detail["objective"] = researchV6ValueOr(payload["task_objective"], researchV6DetailNotRecorded)
		detail["entry_condition"] = map[string]any{"task_id": payload["task_id"], "dispatch_key": payload["dispatch_key"], "dispatched_at": payload["dispatched_at"]}
		detail["method"] = map[string]any{"execution_target": payload["execution_target"], "task_acceptance_criteria": payload["task_acceptance_criteria"]}
		detail["input_artifacts"] = []any{payload["task_id"]}
		detail["actions_taken"] = map[string]any{"runtime_started_at": payload["runtime_started_at"], "runtime_last_observed_at": payload["runtime_last_observed_at"], "result_submitted_at": payload["result_submitted_at"], "completed_at": payload["completed_at"]}
		detail["result"] = map[string]any{"status": payload["attempt_status"], "result_hash": payload["result_hash"], "expected_result": payload["task_expected_result"]}
		detail["decision"] = payload["attempt_status"]
		detail["failure"] = researchV6AttemptFailure(payload)
		detail["recovery"] = researchV6AttemptRecovery(payload)
	case runGraphKindClaim:
		detail["purpose"] = "record_evidence_backed_claim"
		detail["objective"] = researchV6ValueOr(payload["significance"], researchV6NestedValue(payload, "content", "result"))
		detail["entry_condition"] = map[string]any{"produced_by_task_id": payload["produced_by_task_id"], "evidence_standard_key": payload["evidence_standard_key"]}
		detail["method"] = map[string]any{"evidence_standard_key": payload["evidence_standard_key"], "confidence": payload["confidence"]}
		detail["input_artifacts"] = researchV6ClaimObservationIDs(payload["evidence"])
		detail["actions_taken"] = map[string]any{"claim_status": payload["claim_status"]}
		detail["result"] = map[string]any{"claim": researchV6NestedValue(payload, "content", "result"), "confidence": payload["confidence"], "resolution": payload["resolution"]}
		detail["evidence"] = researchV6ValueOr(payload["evidence"], researchV6DetailNotRecorded)
		detail["decision"] = payload["claim_status"]
		detail["failure"] = researchV6ClaimFailure(payload)
		detail["recovery"] = researchV6ClaimRecovery(payload)
	case runGraphKindGate:
		gate, _ := payload["gate"].(map[string]any)
		detail["purpose"] = "evaluate_delivery_gate"
		detail["objective"] = "decide_whether_delivery_contract_is_satisfied"
		detail["entry_condition"] = map[string]any{"phase": payload["phase"]}
		detail["method"] = "canonical_gate_evaluation"
		detail["input_artifacts"] = researchV6ValueOr(payload["claim_ids"], researchV6DetailNotRecorded)
		detail["actions_taken"] = map[string]any{"evaluated": true}
		detail["result"] = researchV6ValueOr(gate, researchV6DetailNotRecorded)
		detail["evidence"] = researchV6ValueOr(gate["findings"], researchV6DetailNotRecorded)
		detail["decision"] = map[string]any{"passed": gate["passed"]}
		detail["failure"] = researchV6GateFailure(gate)
		detail["recovery"] = researchV6GateRecovery(gate)
	}

	for _, field := range researchV6CanonicalDetailFields {
		detail[field] = researchV6ValueOr(detail[field], researchV6DetailNotRecorded)
	}
	return detail
}

func enrichResearchV6TopologyDetails(nodes []researchV6ProjectionNode, edges []researchV6ProjectionEdge) {
	upstream := make(map[string][]string, len(nodes))
	downstream := make(map[string][]string, len(nodes))
	for _, edge := range edges {
		downstream[edge.FromNodeID] = append(downstream[edge.FromNodeID], edge.ToNodeID)
		upstream[edge.ToNodeID] = append(upstream[edge.ToNodeID], edge.FromNodeID)
	}
	for index := range nodes {
		detail, ok := nodes[index].Detail.(map[string]any)
		if !ok {
			continue
		}
		detail["upstream"] = researchV6SortedNodeReferences(upstream[nodes[index].ID])
		detail["downstream"] = researchV6SortedNodeReferences(downstream[nodes[index].ID])
	}
}

func researchV6ActorDetail(actor *string, payload map[string]any) any {
	if actor != nil && strings.TrimSpace(*actor) != "" {
		return *actor
	}
	for _, key := range []string{"assigned_agent_id", "created_by"} {
		if value := strings.TrimSpace(fmt.Sprint(payload[key])); value != "" && value != "<nil>" {
			return value
		}
	}
	return "system"
}

func researchV6NestedValue(payload map[string]any, parent, key string) any {
	nested, _ := payload[parent].(map[string]any)
	return researchV6ValueOr(nested[key], researchV6DetailNotRecorded)
}

func researchV6ValueOr(value, fallback any) any {
	switch typed := value.(type) {
	case nil:
		return fallback
	case string:
		if strings.TrimSpace(typed) == "" {
			return fallback
		}
	case []any:
		if len(typed) == 0 {
			return fallback
		}
	case []string:
		if len(typed) == 0 {
			return fallback
		}
	case map[string]any:
		if len(typed) == 0 {
			return fallback
		}
	}
	return value
}

func researchV6CompactReferences(payload map[string]any, keys ...string) any {
	references := make([]string, 0, len(keys))
	for _, key := range keys {
		value, ok := payload[key].(string)
		if ok && strings.TrimSpace(value) != "" {
			references = append(references, value)
		}
	}
	if len(references) == 0 {
		return researchV6DetailNotApplicable
	}
	return references
}

func researchV6ClaimObservationIDs(raw any) any {
	items, ok := raw.([]any)
	if !ok {
		return researchV6DetailNotRecorded
	}
	ids := make([]string, 0, len(items))
	for _, item := range items {
		evidence, _ := item.(map[string]any)
		if id, ok := evidence["observation_id"].(string); ok && strings.TrimSpace(id) != "" {
			ids = append(ids, id)
		}
	}
	if len(ids) == 0 {
		return researchV6DetailNotRecorded
	}
	sort.Strings(ids)
	return ids
}

func researchV6GateFailure(gate map[string]any) any {
	if passed, _ := gate["passed"].(bool); passed {
		return researchV6DetailNotApplicable
	}
	return researchV6ValueOr(gate["findings"], researchV6DetailNotRecorded)
}

func researchV6RunFailure(payload map[string]any) any {
	status, _ := payload["run_status"].(string)
	if status != "failed" && status != "cancelled" {
		return researchV6DetailNotApplicable
	}
	return map[string]any{
		"status":      status,
		"last_error":  researchV6ValueOr(payload["last_error"], researchV6DetailNotRecorded),
		"stop_reason": researchV6ValueOr(payload["stop_reason"], researchV6DetailNotRecorded),
	}
}

func researchV6TaskFailure(payload map[string]any) any {
	status, _ := payload["task_status"].(string)
	if status != "failed" && status != "blocked" && status != "cancelled" {
		return researchV6DetailNotApplicable
	}
	return map[string]any{"status": status, "terminal_reason": researchV6ValueOr(payload["terminal_reason"], researchV6DetailNotRecorded)}
}

func researchV6AttemptFailure(payload map[string]any) any {
	status, _ := payload["attempt_status"].(string)
	if status != "failed" && status != "lost" && status != "cancelled" {
		return researchV6DetailNotApplicable
	}
	return map[string]any{
		"status":                status,
		"failure_class":         researchV6ValueOr(payload["failure_class"], researchV6DetailNotRecorded),
		"source_failure_reason": researchV6ValueOr(payload["source_failure_reason"], researchV6DetailNotRecorded),
		"diagnostics":           researchV6ValueOr(payload["diagnostics"], researchV6DetailNotRecorded),
	}
}

func researchV6AttemptRecovery(payload map[string]any) any {
	status, _ := payload["attempt_status"].(string)
	pending, _ := payload["pending_failure_retryable"].(bool)
	if status != "failed" && status != "lost" && status != "cancelled" && status != "cancelling" && !pending {
		return researchV6DetailNotApplicable
	}
	return map[string]any{
		"pending_failure_class":       researchV6ValueOr(payload["pending_failure_class"], researchV6DetailNotRecorded),
		"pending_failure_diagnostics": researchV6ValueOr(payload["pending_failure_diagnostics"], researchV6DetailNotRecorded),
		"pending_failure_retryable":   pending,
		"cancellation_requested_at":   researchV6ValueOr(payload["cancellation_requested_at"], researchV6DetailNotRecorded),
	}
}

func researchV6QuestionFailure(payload map[string]any) any {
	status, _ := payload["question_status"].(string)
	if status != "unresolved" && status != "obsolete" {
		return researchV6DetailNotApplicable
	}
	return researchV6ValueOr(payload["terminal_explanation"], researchV6DetailNotRecorded)
}

func researchV6QuestionRecovery(payload map[string]any) any {
	status, _ := payload["question_status"].(string)
	if status != "unresolved" && status != "obsolete" {
		return researchV6DetailNotApplicable
	}
	return map[string]any{"status": status, "required": payload["required"], "terminal_explanation": payload["terminal_explanation"]}
}

func researchV6ClaimFailure(payload map[string]any) any {
	status, _ := payload["claim_status"].(string)
	switch status {
	case "disputed", "refuted", "unresolved", "superseded":
		return map[string]any{"status": status, "resolution": researchV6ValueOr(payload["resolution"], researchV6DetailNotRecorded)}
	default:
		return researchV6DetailNotApplicable
	}
}

func researchV6ClaimRecovery(payload map[string]any) any {
	status, _ := payload["claim_status"].(string)
	switch status {
	case "disputed", "refuted", "unresolved", "superseded":
		return map[string]any{"status": status, "resolution": researchV6ValueOr(payload["resolution"], researchV6DetailNotRecorded)}
	default:
		return researchV6DetailNotApplicable
	}
}

func researchV6GateRecovery(gate map[string]any) any {
	if passed, _ := gate["passed"].(bool); passed {
		return researchV6DetailNotApplicable
	}
	return map[string]any{"required": true, "findings": researchV6ValueOr(gate["findings"], researchV6DetailNotRecorded)}
}

func researchV6SortedNodeReferences(ids []string) any {
	if len(ids) == 0 {
		return researchV6DetailNotApplicable
	}
	sort.Strings(ids)
	return ids
}
