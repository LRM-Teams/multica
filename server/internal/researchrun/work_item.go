package researchrun

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"
)

var (
	ErrWorkItemChanged   = errors.New("research work item changed")
	ErrWorkItemLeaseLost = errors.New("research work item lease lost")
)

type ClaimV6WorkItemInput struct {
	WorkspaceID, RunID, WorkItemID string
	ExpectedStateVersion           int64
	LeaseToken                     string
	Now                            time.Time
	LeaseDuration                  time.Duration
}

type V6WorkItemLease struct {
	WorkItem  V6WorkItem
	Token     string
	ExpiresAt time.Time
}

type CompleteV6WorkItemInput struct {
	WorkspaceID, RunID, WorkItemID string
	ExpectedStateVersion           int64
	LeaseToken                     string
}

type workItemStore interface {
	ClaimV6WorkItem(context.Context, ClaimV6WorkItemInput) (V6WorkItemLease, error)
	CompleteV6WorkItem(context.Context, CompleteV6WorkItemInput) (V6WorkItem, error)
}

type workItemModule struct{ store workItemStore }

func (m workItemModule) Claim(ctx context.Context, in ClaimV6WorkItemInput) (V6WorkItemLease, error) {
	if m.store == nil || strings.TrimSpace(in.WorkItemID) == "" || strings.TrimSpace(in.LeaseToken) == "" || in.ExpectedStateVersion < 1 || in.LeaseDuration <= 0 {
		return V6WorkItemLease{}, fmt.Errorf("%w: invalid claim", ErrInvalidContract)
	}
	if in.Now.IsZero() {
		return V6WorkItemLease{}, fmt.Errorf("%w: claim time is required", ErrInvalidContract)
	}
	return m.store.ClaimV6WorkItem(ctx, in)
}

func (m workItemModule) Complete(ctx context.Context, in CompleteV6WorkItemInput) (V6WorkItem, error) {
	if m.store == nil || strings.TrimSpace(in.WorkItemID) == "" || strings.TrimSpace(in.LeaseToken) == "" || in.ExpectedStateVersion < 1 {
		return V6WorkItem{}, fmt.Errorf("%w: invalid completion", ErrInvalidContract)
	}
	return m.store.CompleteV6WorkItem(ctx, in)
}
