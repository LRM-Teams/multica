package researchrun

import (
	"fmt"
	"strconv"
	"strings"
	"time"
)

// TargetRepair is the durable record of the one repair action chosen for one
// canonical execution failure of one Task at one state version. It is the
// answer to "have we already decided how to repair exactly this failure?" and
// exists so a recomputed failure reuses the decision instead of creating a
// second remediation path for the same cause.
type TargetRepair struct {
	ID                      string
	WorkspaceID             string
	SessionID               string
	TaskID                  string
	GoalVersion             int
	PlanVersion             int
	FailureClass            FailureClass
	FailureFingerprint      string
	RepairKind              RepairKind
	RepairKey               string
	SourceReason            string
	TargetConfigFingerprint string
	Diagnostics             string
	OccurrenceCount         int
	FirstAttemptID          string
	LastAttemptID           string
	FirstObservedAt         time.Time
	LastObservedAt          time.Time
}

// allowedRepairActions is the executor's action matrix. It mirrors the
// immutable database judgement research_repair_action_allowed; the database is
// the runtime single source of truth and this map exists so the executor can
// refuse an illegal action before opening a write.
//
// Three classes deliberately have no entry, so no repair can ever be recorded
// for them. research_negative and method_invalid are research outcomes, not
// execution failures. internal_invariant must fail closed to a human and is
// forbidden from fabricating recovery data, so licensing any automated repair
// action for it would be wrong.
var allowedRepairActions = map[FailureClass][]RepairKind{
	FailureRuntimeLost:     {RepairWaitForTarget, RepairRerouteTarget},
	FailureTimeout:         {RepairRetryTarget, RepairRerouteTarget},
	FailureRateLimited:     {RepairWaitForTarget, RepairRerouteTarget},
	FailureProvider:        {RepairRerouteTarget, RepairWaitForTarget},
	FailureNetwork:         {RepairRerouteTarget, RepairWaitForTarget},
	FailureCredential:      {RepairRequestConfiguration, RepairWaitForTarget},
	FailureConfiguration:   {RepairRequestConfiguration},
	FailureTool:            {RepairRerouteTarget, RepairRequestConfiguration},
	FailureResultInvalid:   {RepairFreshSession},
	FailureContractBlocked: {RepairRequestDecision},
	FailurePermission:      {RepairRequestDecision},
	FailureCapability:      {RepairRequestDecision},
	FailureTargetChanged:   {RepairRerouteTarget},
	FailureUnknown:         {RepairRetryTarget, RepairRerouteTarget},
}

// RepairActionAllowed reports whether the failure class permits the action.
func RepairActionAllowed(class FailureClass, kind RepairKind) bool {
	for _, allowed := range allowedRepairActions[class] {
		if allowed == kind {
			return true
		}
	}
	return false
}

// AllowedRepairActions returns the actions the failure class permits. The
// returned slice is a copy so callers cannot mutate the matrix.
func AllowedRepairActions(class FailureClass) []RepairKind {
	allowed := allowedRepairActions[class]
	if len(allowed) == 0 {
		return nil
	}
	return append(make([]RepairKind, 0, len(allowed)), allowed...)
}

// FailureFingerprint is the canonical identity of one execution failure cause.
// The frozen target configuration participates so a configuration change
// produces a genuinely new repair instead of reusing a decision that was made
// against a target that no longer exists.
func FailureFingerprint(class FailureClass, sourceReason, targetConfigFingerprint string) string {
	return ExecutionTargetFingerprint(
		strings.ToLower(strings.TrimSpace(string(class))),
		strings.ToLower(strings.TrimSpace(sourceReason)),
		strings.TrimSpace(targetConfigFingerprint),
	)
}

// RepairKeyFor builds the target-idempotent repair key. Recomputing the same
// canonical failure at the same state version yields the same key, so the
// unique constraint reuses the existing record. Only a state version, target
// configuration, or failure class change moves the key.
func RepairKeyFor(sessionID, taskID string, goalVersion, planVersion int, failureFingerprint string, kind RepairKind) string {
	return strings.Join([]string{
		"research-repair",
		strings.TrimSpace(sessionID),
		strings.TrimSpace(taskID),
		strconv.Itoa(goalVersion),
		strconv.Itoa(planVersion),
		strings.TrimSpace(failureFingerprint),
		strings.TrimSpace(string(kind)),
	}, ":")
}

// repairDecisionFor resolves the durable repair decision for a classified
// execution failure. ok is false when the class carries no repair action, which
// is the research-outcome case and must not be recorded as an execution repair.
func repairDecisionFor(class FailureClass, sourceReason, targetConfigFingerprint string) (RepairKind, string, bool, error) {
	kind := failureDisposition(class).Repair
	if kind == RepairNone {
		return RepairNone, "", false, nil
	}
	if !RepairActionAllowed(class, kind) {
		return RepairNone, "", false, fmt.Errorf(
			"%w: repair action %q is not allowed for failure class %q",
			ErrInvalidTransition, kind, class,
		)
	}
	return kind, FailureFingerprint(class, sourceReason, targetConfigFingerprint), true, nil
}
