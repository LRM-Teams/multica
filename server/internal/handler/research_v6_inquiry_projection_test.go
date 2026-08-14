package handler

import (
	"testing"
	"time"
)

func TestCanonicalResearchV6InquiryNodeUsesStableV6IdentityAndVersions(t *testing.T) {
	now := time.Date(2026, 8, 14, 1, 2, 3, 0, time.UTC)
	node := canonicalResearchV6InquiryNode("run", "question", "q1", "gap", "What changed?", "open", .9, 2, 3, now, now, map[string]any{"required": true})
	if node.ID != "run:question:q1" || node.SchemaVersion != 6 || node.ContractVersion == nil || *node.ContractVersion != "2" || node.PlanVersion == nil || *node.PlanVersion != "3" || node.Freshness == nil || *node.Freshness != "fresh" {
		t.Fatalf("node=%+v", node)
	}
}

func TestJSONValuePreservesCanonicalStructuredFields(t *testing.T) {
	value := jsonValue([]byte(`{"region":"CN"}`))
	object, ok := value.(map[string]any)
	if !ok || object["region"] != "CN" {
		t.Fatalf("value=%+v", value)
	}
}
