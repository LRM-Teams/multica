package researchrun

import (
	"context"
	"errors"
	"testing"
	"time"
)

type workItemStoreStub struct {
	item V6WorkItem
}

func (s *workItemStoreStub) ClaimV6WorkItem(_ context.Context, in ClaimV6WorkItemInput) (V6WorkItemLease, error) {
	if s.item.StateVersion != in.ExpectedStateVersion {
		return V6WorkItemLease{}, ErrWorkItemChanged
	}
	s.item.StateVersion++
	s.item.Status = V6WorkRunning
	return V6WorkItemLease{WorkItem: s.item, Token: in.LeaseToken, ExpiresAt: in.Now.Add(in.LeaseDuration)}, nil
}

func (s *workItemStoreStub) CompleteV6WorkItem(_ context.Context, in CompleteV6WorkItemInput) (V6WorkItem, error) {
	if in.LeaseToken != "new-owner" {
		return V6WorkItem{}, ErrWorkItemLeaseLost
	}
	s.item.StateVersion++
	s.item.Status = V6WorkSucceeded
	return s.item, nil
}

func TestWorkItemModuleUsesOneLeaseSurfaceForEveryV6Kind(t *testing.T) {
	for _, kind := range allV6WorkItemKinds() {
		t.Run(string(kind), func(t *testing.T) {
			store := &workItemStoreStub{item: V6WorkItem{ID: "work", Kind: kind, Status: V6WorkReady, StateVersion: 7}}
			module := workItemModule{store: store}
			lease, err := module.Claim(context.Background(), ClaimV6WorkItemInput{
				WorkItemID: "work", ExpectedStateVersion: 7, LeaseToken: "new-owner",
				Now: time.Unix(100, 0), LeaseDuration: time.Minute,
			})
			if err != nil {
				t.Fatal(err)
			}
			if lease.WorkItem.Kind != kind || lease.WorkItem.Status != V6WorkRunning || lease.WorkItem.StateVersion != 8 {
				t.Fatalf("lease=%+v", lease)
			}
			if _, err = module.Complete(context.Background(), CompleteV6WorkItemInput{
				WorkItemID: "work", ExpectedStateVersion: 8, LeaseToken: "expired-owner",
			}); !errors.Is(err, ErrWorkItemLeaseLost) {
				t.Fatalf("stale owner completion error=%v", err)
			}
			completed, err := module.Complete(context.Background(), CompleteV6WorkItemInput{
				WorkItemID: "work", ExpectedStateVersion: 8, LeaseToken: "new-owner",
			})
			if err != nil || completed.Status != V6WorkSucceeded || completed.StateVersion != 9 {
				t.Fatalf("completed=%+v err=%v", completed, err)
			}
		})
	}
}
