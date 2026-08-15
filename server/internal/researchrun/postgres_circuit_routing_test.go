package researchrun

import (
	"testing"
	"time"
)

func TestProviderLockBlocksExecutionTargetRejectsBlankJSONPlaceholder(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 8, 13, 9, 0, 0, 0, time.UTC)

	for _, detail := range []string{"", "{}", "{ }", "[]", "null", `""`} {
		if providerLockBlocksExecutionTarget(detail, nil, now) {
			t.Fatalf("placeholder provider detail %q must not block dispatch", detail)
		}
	}
	if !providerLockBlocksExecutionTarget("quota exhausted", nil, now) {
		t.Fatal("a real provider lock with unknown expiry must block dispatch")
	}
	future := now.Add(time.Hour)
	if !providerLockBlocksExecutionTarget("quota exhausted", &future, now) {
		t.Fatal("a real provider lock before its expiry must block dispatch")
	}
	past := now.Add(-time.Second)
	if providerLockBlocksExecutionTarget("quota exhausted", &past, now) {
		t.Fatal("an expired provider lock must not block dispatch")
	}
}
