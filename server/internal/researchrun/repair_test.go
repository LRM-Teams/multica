package researchrun

import (
	"strings"
	"testing"

	"github.com/multica-ai/multica/server/pkg/taskfailure"
)

// Every classified execution failure must choose an action its own class
// permits. Without this lock the disposition table and the allowed action
// matrix can drift apart and the executor would persist an action the failure
// class does not license.
func TestFailureDispositionOnlyChoosesAllowedRepairActions(t *testing.T) {
	classes := []FailureClass{
		FailureResearchNegative, FailureMethodInvalid, FailureContractBlocked,
		FailureResultInvalid, FailurePermission, FailureCredential,
		FailureRateLimited, FailureNetwork, FailureTimeout, FailureTool,
		FailureProvider, FailureRuntimeLost, FailureConfiguration,
		FailureCapability, FailureTargetChanged, FailureInternal, FailureUnknown,
	}
	for _, class := range classes {
		chosen := failureDisposition(class).Repair
		if chosen == RepairNone {
			if actions := AllowedRepairActions(class); len(actions) != 0 {
				t.Fatalf("class %q chooses no repair but allows %v", class, actions)
			}
			continue
		}
		if !RepairActionAllowed(class, chosen) {
			t.Fatalf("class %q chooses disallowed repair %q (allowed: %v)",
				class, chosen, AllowedRepairActions(class))
		}
	}
}

// Research outcomes are not execution failures, and an internal invariant
// breach must fail closed to a human rather than fabricate recovery data. All
// three must carry no repair action at all, otherwise a refuted hypothesis or a
// corrupted state machine would be "repaired" automatically.
func TestClassesWithoutLicensedRepairRecordNothing(t *testing.T) {
	for _, class := range []FailureClass{FailureResearchNegative, FailureMethodInvalid, FailureInternal} {
		if actions := AllowedRepairActions(class); len(actions) != 0 {
			t.Fatalf("class %q allows repair actions %v", class, actions)
		}
		if _, _, recordable, err := repairDecisionFor(class, "reason", "fingerprint"); err != nil || recordable {
			t.Fatalf("class %q recordable=%v err=%v", class, recordable, err)
		}
	}
}

// The production failure path is the durable Agent Inbox reason. Every reason
// must resolve to an action its own class licenses, so a real provider or
// runtime failure can never persist an unlicensed repair.
func TestEveryDurableInboxFailureReasonResolvesToAllowedRepair(t *testing.T) {
	reasons := taskfailure.AllReasons()
	if len(reasons) == 0 {
		t.Fatal("no durable inbox failure reasons to check")
	}
	for _, reason := range reasons {
		for _, runtimeRetryable := range []bool{true, false} {
			disposition := ClassifyInboxFailure(string(reason), runtimeRetryable)
			if disposition.Repair == RepairNone {
				if actions := AllowedRepairActions(disposition.Class); len(actions) != 0 {
					t.Fatalf("reason %q (class %q) chooses no repair but allows %v",
						reason, disposition.Class, actions)
				}
				continue
			}
			if !RepairActionAllowed(disposition.Class, disposition.Repair) {
				t.Fatalf("reason %q (class %q, retryable=%v) chooses disallowed repair %q (allowed: %v)",
					reason, disposition.Class, runtimeRetryable, disposition.Repair,
					AllowedRepairActions(disposition.Class))
			}
		}
	}
}

// Control group: an unknown reason string still classifies and still resolves
// to a licensed action, so the sweep above cannot pass by classifying nothing.
func TestUnknownInboxFailureReasonStillResolvesToAllowedRepair(t *testing.T) {
	disposition := ClassifyInboxFailure("totally_new_provider_error", true)
	if disposition.Class != FailureUnknown {
		t.Fatalf("unknown reason class=%q", disposition.Class)
	}
	if !RepairActionAllowed(disposition.Class, disposition.Repair) {
		t.Fatalf("unknown reason chose disallowed repair %q", disposition.Repair)
	}
}

// Control group: a genuine execution failure must be recordable, so the tests
// above cannot pass merely because nothing is ever recordable.
func TestExecutionFailureClassIsRecordable(t *testing.T) {
	kind, fingerprint, recordable, err := repairDecisionFor(FailureCredential, "provider_auth", "config-a")
	if err != nil || !recordable {
		t.Fatalf("credential failure recordable=%v err=%v", recordable, err)
	}
	if kind != RepairRequestConfiguration {
		t.Fatalf("credential repair kind=%q", kind)
	}
	if strings.TrimSpace(fingerprint) == "" {
		t.Fatal("credential failure produced an empty fingerprint")
	}
}

// The repair key is the idempotency unit. Recomputing the same canonical
// failure at the same state version must reuse it; a changed state version,
// target configuration, or failure class must move it.
func TestRepairKeyIsStableAndMovesWithCanonicalIdentity(t *testing.T) {
	fingerprint := FailureFingerprint(FailureTimeout, "agent_timeout", "config-a")
	base := RepairKeyFor("session-1", "task-1", 2, 3, fingerprint, RepairRetryTarget)

	if repeat := RepairKeyFor("session-1", "task-1", 2, 3, fingerprint, RepairRetryTarget); repeat != base {
		t.Fatalf("recomputed key drifted: %q != %q", repeat, base)
	}

	cases := map[string]string{
		"goal version": RepairKeyFor("session-1", "task-1", 3, 3, fingerprint, RepairRetryTarget),
		"plan version": RepairKeyFor("session-1", "task-1", 2, 4, fingerprint, RepairRetryTarget),
		"task":         RepairKeyFor("session-1", "task-2", 2, 3, fingerprint, RepairRetryTarget),
		"session":      RepairKeyFor("session-2", "task-1", 2, 3, fingerprint, RepairRetryTarget),
		"repair kind":  RepairKeyFor("session-1", "task-1", 2, 3, fingerprint, RepairRerouteTarget),
		"target config": RepairKeyFor("session-1", "task-1", 2, 3,
			FailureFingerprint(FailureTimeout, "agent_timeout", "config-b"), RepairRetryTarget),
		"failure class": RepairKeyFor("session-1", "task-1", 2, 3,
			FailureFingerprint(FailureNetwork, "agent_timeout", "config-a"), RepairRetryTarget),
	}
	for label, moved := range cases {
		if moved == base {
			t.Fatalf("%s change did not move the repair key", label)
		}
	}
}

// The fingerprint must not collapse distinct causes into one repair, and must
// not split one cause into two because of incidental whitespace or case.
func TestFailureFingerprintNormalisesWithoutCollapsingCauses(t *testing.T) {
	canonical := FailureFingerprint(FailureProvider, "provider_server_error", "config-a")
	if noisy := FailureFingerprint(FailureProvider, "  Provider_Server_Error ", " config-a "); noisy != canonical {
		t.Fatalf("whitespace/case variation split one cause into two fingerprints")
	}
	if other := FailureFingerprint(FailureProvider, "provider_network", "config-a"); other == canonical {
		t.Fatal("distinct source reasons collapsed into one fingerprint")
	}
}

// AllowedRepairActions hands out a copy; a caller cannot widen the matrix.
func TestAllowedRepairActionsCannotBeMutatedByCallers(t *testing.T) {
	actions := AllowedRepairActions(FailureConfiguration)
	if len(actions) != 1 || actions[0] != RepairRequestConfiguration {
		t.Fatalf("configuration actions=%v", actions)
	}
	actions[0] = RepairRerouteTarget
	if RepairActionAllowed(FailureConfiguration, RepairRerouteTarget) {
		t.Fatal("caller mutation widened the allowed action matrix")
	}
}
