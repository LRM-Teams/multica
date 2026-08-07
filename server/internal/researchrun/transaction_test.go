package researchrun

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"github.com/jackc/pgx/v5"
)

type recordingResearchTx struct {
	pgx.Tx
	events      *[]string
	commitErr   error
	rollbackErr error
	commits     int
	rollbacks   int
}

func (tx *recordingResearchTx) Commit(context.Context) error {
	tx.commits++
	*tx.events = append(*tx.events, "commit")
	return tx.commitErr
}

func (tx *recordingResearchTx) Rollback(context.Context) error {
	tx.rollbacks++
	*tx.events = append(*tx.events, "rollback")
	return tx.rollbackErr
}

func TestResearchTransactionAfterBeginFaultRollsBack(t *testing.T) {
	ctx := context.Background()
	injected := errors.New("after begin fault")
	events := []string{}
	tx := &recordingResearchTx{events: &events}
	begin := func(context.Context, pgx.TxOptions) (pgx.Tx, error) {
		events = append(events, "begin")
		return tx, nil
	}
	hook := func(_ context.Context, operation researchTxOperation, point researchTxFaultPoint) error {
		if operation != txOpDispatchIntentCreate {
			t.Fatalf("operation=%q", operation)
		}
		events = append(events, "hook:"+string(point))
		if point == txAfterBegin {
			return injected
		}
		return nil
	}

	gotTx, err := beginResearchTx(ctx, txOpDispatchIntentCreate, pgx.TxOptions{}, begin, hook)

	if gotTx != nil {
		t.Fatalf("transaction=%v, want nil", gotTx)
	}
	if !errors.Is(err, injected) {
		t.Fatalf("error=%v, want injected fault", err)
	}
	if tx.rollbacks != 1 || tx.commits != 0 {
		t.Fatalf("commits=%d rollbacks=%d", tx.commits, tx.rollbacks)
	}
	wantEvents := []string{"begin", "hook:after_begin", "rollback"}
	if !reflect.DeepEqual(events, wantEvents) {
		t.Fatalf("events=%v want=%v", events, wantEvents)
	}
}

func TestResearchTransactionBeforeCommitFaultDefersToRollback(t *testing.T) {
	ctx := context.Background()
	injected := errors.New("before commit fault")
	events := []string{}
	tx := &recordingResearchTx{events: &events}
	hook := func(_ context.Context, operation researchTxOperation, point researchTxFaultPoint) error {
		if operation != txOpDispatchIntentCreate {
			t.Fatalf("operation=%q", operation)
		}
		events = append(events, "hook:"+string(point))
		if point == txBeforeCommit {
			return injected
		}
		return nil
	}

	var err error
	func() {
		defer tx.Rollback(ctx)
		err = commitResearchTx(ctx, txOpDispatchIntentCreate, tx, hook)
	}()

	if !errors.Is(err, injected) {
		t.Fatalf("error=%v, want injected fault", err)
	}
	if errors.Is(err, ErrCommitOutcomeUnknown) {
		t.Fatalf("before-commit error was marked unknown outcome: %v", err)
	}
	if tx.rollbacks != 1 || tx.commits != 0 {
		t.Fatalf("commits=%d rollbacks=%d", tx.commits, tx.rollbacks)
	}
	wantEvents := []string{"hook:before_commit", "rollback"}
	if !reflect.DeepEqual(events, wantEvents) {
		t.Fatalf("events=%v want=%v", events, wantEvents)
	}
}

func TestResearchTransactionAfterCommitFaultReturnsUnknownOutcome(t *testing.T) {
	ctx := context.Background()
	injected := errors.New("after commit fault")
	events := []string{}
	tx := &recordingResearchTx{events: &events}
	hook := func(_ context.Context, operation researchTxOperation, point researchTxFaultPoint) error {
		if operation != txOpDispatchIntentCreate {
			t.Fatalf("operation=%q", operation)
		}
		events = append(events, "hook:"+string(point))
		if point == txAfterCommit {
			return injected
		}
		return nil
	}

	err := commitResearchTx(ctx, txOpDispatchIntentCreate, tx, hook)

	if !errors.Is(err, ErrCommitOutcomeUnknown) {
		t.Fatalf("error=%v, want unknown outcome sentinel", err)
	}
	if !errors.Is(err, injected) {
		t.Fatalf("error=%v, want injected fault", err)
	}
	if tx.commits != 1 || tx.rollbacks != 0 {
		t.Fatalf("commits=%d rollbacks=%d", tx.commits, tx.rollbacks)
	}
	wantEvents := []string{"hook:before_commit", "commit", "hook:after_commit"}
	if !reflect.DeepEqual(events, wantEvents) {
		t.Fatalf("events=%v want=%v", events, wantEvents)
	}

	realCommitErr := errors.New("database commit failed")
	events = nil
	tx = &recordingResearchTx{events: &events, commitErr: realCommitErr}
	err = commitResearchTx(ctx, txOpDispatchIntentCreate, tx, hook)
	if !errors.Is(err, realCommitErr) {
		t.Fatalf("commit error=%v, want database error", err)
	}
	if errors.Is(err, ErrCommitOutcomeUnknown) {
		t.Fatalf("database commit error was marked injected unknown outcome: %v", err)
	}
	wantEvents = []string{"hook:before_commit", "commit"}
	if !reflect.DeepEqual(events, wantEvents) {
		t.Fatalf("commit failure events=%v want=%v", events, wantEvents)
	}
}
