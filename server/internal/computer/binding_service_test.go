package computer

import (
	"sync"
	"testing"
)

// fakeBindingStore is an in-memory BindingStore for service-level tests.
type fakeBindingStore struct {
	mu    sync.Mutex
	items map[string]WorkspaceBinding
}

func newFakeBindingStore() *fakeBindingStore { return &fakeBindingStore{items: map[string]WorkspaceBinding{}} }

func (f *fakeBindingStore) Get(w string) (WorkspaceBinding, bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	b, ok := f.items[w]
	return b, ok, nil
}
func (f *fakeBindingStore) AddOrRepair(b WorkspaceBinding) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.items[b.WorkspaceID] = b
	return nil
}
func (f *fakeBindingStore) Remove(w string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	delete(f.items, w)
	return nil
}
func (f *fakeBindingStore) All() ([]WorkspaceBinding, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]WorkspaceBinding, 0, len(f.items))
	for _, b := range f.items {
		out = append(out, b)
	}
	return out, nil
}

func acts() BindingRequest { return BindingRequest{ActorUserID: "u", TargetComputerID: "c", TargetWorkspaceID: "ws"} }

func TestBindingServiceCreateFreshThenRepairIdempotent(t *testing.T) {
	svc := &BindingService{Store: newFakeBindingStore()}
	req := acts()

	r1, err := svc.Create(req, WorkspaceBinding{WorkspaceID: "ws", ComputerID: "c"})
	if err != nil || r1.Kind != ValidationKindCreate {
		t.Fatalf("first create = %+v, %v; want kind Create", r1, err)
	}
	r2, err := svc.Create(req, WorkspaceBinding{WorkspaceID: "ws", ComputerID: "c"})
	if err != nil || r2.Kind != ValidationKindRepair {
		t.Fatalf("repeat create = kind %v, err %v; want Repair (no duplicate)", r2.Kind, err)
	}
	all, _ := svc.All()
	if len(all) != 1 {
		t.Fatalf("service created %d binding(s), want 1 (idempotent)", len(all))
	}
}

func TestBindingServiceCreateRejectsMissingIds(t *testing.T) {
	svc := &BindingService{Store: newFakeBindingStore()}
	bad := BindingRequest{} // no actor/computer/workspace
	if _, err := svc.Create(bad, WorkspaceBinding{}); err == nil {
		t.Fatal("create with missing ids should fail closed")
	}
}

func TestBindingServiceRevokeOnePreservesSiblings(t *testing.T) {
	svc := &BindingService{Store: newFakeBindingStore()}
	svc.Create(BindingRequest{ActorUserID: "u", TargetComputerID: "c", TargetWorkspaceID: "ws-1"}, WorkspaceBinding{WorkspaceID: "ws-1", ComputerID: "c"})
	req2 := BindingRequest{ActorUserID: "u", TargetComputerID: "c", TargetWorkspaceID: "ws-2"}
	svc.Create(req2, WorkspaceBinding{WorkspaceID: "ws-2", ComputerID: "c"})

	if err := svc.Revoke(BindingRequest{ActorUserID: "u", TargetComputerID: "c", TargetWorkspaceID: "ws-1"}, "ws-1"); err != nil {
		t.Fatalf("revoke ws-1: %v", err)
	}
	all, _ := svc.All()
	if len(all) != 1 || all[0].WorkspaceID != "ws-2" {
		t.Fatalf("revoking ws-1 disturbed sibling: %+v", all)
	}
}

func TestBindingServiceRevokeFailsClosed(t *testing.T) {
	svc := &BindingService{Store: newFakeBindingStore()}
	svc.Create(acts(), WorkspaceBinding{WorkspaceID: "ws", ComputerID: "c"})
	// Cross-workspace target not present → fail closed.
	if err := svc.Revoke(BindingRequest{ActorUserID: "u", TargetComputerID: "c", TargetWorkspaceID: "missing"}, "missing"); err == nil {
		t.Fatal("revoking a non-existent binding must fail closed")
	}
	// No actor → fail closed.
	if err := svc.Revoke(BindingRequest{TargetComputerID: "c", TargetWorkspaceID: "ws"}, "ws"); err == nil {
		t.Fatal("revoke without actor must fail closed")
	}
}
