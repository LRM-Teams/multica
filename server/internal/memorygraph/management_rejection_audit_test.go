package memorygraph

import "testing"

// Spec §13: rejected management operations must be durable audit data with a
// machine-readable reason, not merely an in-memory ConsolidateResult field.
func TestManagementRejectionIsAppendedToOperationAudit(t *testing.T) {
	store := newTestStore(t)
	c := NewConsolidator(store, nil, DefaultConsolidateConfig(), testConsolidateScope(), nil, nil)
	g := newGraph()
	_, rejected, err := c.applyOperations(g, 1, CreatorConsolidator, []ConsolidateOp{{Op: "not_an_operation"}})
	if err != nil {
		t.Fatal(err)
	}
	if len(rejected) != 1 {
		t.Fatalf("rejected = %+v, want one", rejected)
	}
	entries, err := c.oplog.Read(1)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 || entries[0].Op != OpRejectedManagement || entries[0].Detail["reason"] != rejected[0].Reason {
		t.Fatalf("rejection audit = %+v, want durable reason", entries)
	}
}
