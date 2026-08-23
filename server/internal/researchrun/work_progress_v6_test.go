package researchrun

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
)

type workProgressStoreStub struct {
	got  []ReportV6WorkProgressInput
	fail error
}

func (s *workProgressStoreStub) ReportV6WorkProgress(_ context.Context, in ReportV6WorkProgressInput) error {
	if s.fail != nil {
		return s.fail
	}
	s.got = append(s.got, in)
	return nil
}

func TestV6WorkProgressModuleValidation(t *testing.T) {
	ctx := context.Background()
	access := V6AttemptAccess{
		WorkspaceID: uuid.NewString(), RunID: uuid.NewString(),
		WorkItemID: uuid.NewString(), AttemptID: uuid.NewString(), AgentID: uuid.NewString(),
	}

	if err := (workProgressModule{}).Report(ctx, ReportV6WorkProgressInput{
		V6AttemptAccess: access, ClientRequestID: uuid.NewString(), Text: "reading sources",
	}); !errors.Is(err, ErrInvalidContract) {
		t.Fatalf("nil store error=%v, want ErrInvalidContract", err)
	}

	store := &workProgressStoreStub{}
	module := workProgressModule{store: store}

	for name, in := range map[string]ReportV6WorkProgressInput{
		"missing text":       {V6AttemptAccess: access, ClientRequestID: uuid.NewString(), Text: "   "},
		"missing request id": {V6AttemptAccess: access, ClientRequestID: " ", Text: "searching"},
		"oversized stage":    {V6AttemptAccess: access, ClientRequestID: uuid.NewString(), Text: "searching", Stage: strings.Repeat("s", maxV6WorkProgressStageRunes+1)},
	} {
		if err := module.Report(ctx, in); !errors.Is(err, ErrInvalidContract) {
			t.Fatalf("%s: error=%v, want ErrInvalidContract", name, err)
		}
	}
	if len(store.got) != 0 {
		t.Fatalf("store received %d invalid reports", len(store.got))
	}

	// Long note text is truncated, not rejected: notes are UI captions and a
	// verbose Agent must not lose its progress signal over length.
	long := strings.Repeat("进", maxV6WorkProgressTextRunes+50)
	if err := module.Report(ctx, ReportV6WorkProgressInput{
		V6AttemptAccess: access, ClientRequestID: uuid.NewString(), Text: long, Stage: " searching ",
	}); err != nil {
		t.Fatal(err)
	}
	if len(store.got) != 1 {
		t.Fatalf("store received %d reports, want 1", len(store.got))
	}
	if runes := []rune(store.got[0].Text); len(runes) != maxV6WorkProgressTextRunes {
		t.Fatalf("text runes=%d, want %d", len(runes), maxV6WorkProgressTextRunes)
	}
	if store.got[0].Stage != "searching" {
		t.Fatalf("stage=%q, want trimmed", store.got[0].Stage)
	}
}

func TestV6WorkProgressModuleReplacesPureEnglishNarration(t *testing.T) {
	store := &workProgressStoreStub{}
	module := workProgressModule{store: store}
	access := V6AttemptAccess{
		WorkspaceID: uuid.NewString(), RunID: uuid.NewString(),
		WorkItemID: uuid.NewString(), AttemptID: uuid.NewString(), AgentID: uuid.NewString(),
	}

	if err := module.Report(context.Background(), ReportV6WorkProgressInput{
		V6AttemptAccess: access,
		ClientRequestID: uuid.NewString(),
		Text:            "Now let me inspect the manifest and schema.",
		Stage:           "reading_manifest",
	}); err != nil {
		t.Fatal(err)
	}
	if len(store.got) != 1 || store.got[0].Text != v6WorkProgressChineseFallback {
		t.Fatalf("progress=%+v, want Chinese fallback", store.got)
	}
}

func TestReportV6WorkProgressTransactionRecovery(t *testing.T) {
	runTransactionRecoveryMatrix(t, txOpV6WorkProgressReport, func(t *testing.T, run *transactionRecoveryRun) transactionRecoveryOperation {
		membershipID, workItemID := seedV6RecoveryWorkItem(t, run, "running", time.Now().Add(time.Minute))
		attemptID := seedV6RecoveryAttempt(t, run, membershipID, workItemID)
		input := ReportV6WorkProgressInput{
			V6AttemptAccess: V6AttemptAccess{
				WorkspaceID: run.fixture.workspaceID, RunID: run.fixture.sessionID,
				WorkItemID: workItemID, AttemptID: attemptID, AgentID: run.fixture.agentID,
			},
			ClientRequestID: uuid.NewString(), Text: "正在检索一手来源", Stage: "searching",
		}
		invoke := func() error { return run.store.ReportV6WorkProgress(run.ctx, input) }
		count := func() int {
			var value int
			if err := run.pool.QueryRow(run.ctx, `
				SELECT count(*)::int FROM research_run_event
				WHERE session_id=$1::uuid AND event_type=$2 AND payload->>'attempt_id'=$3
			`, run.fixture.sessionID, V6WorkProgressEventType, attemptID).Scan(&value); err != nil {
				t.Fatal(err)
			}
			return value
		}
		return transactionRecoveryOperation{invoke: invoke,
			assertRolledBack: func() {
				if got := count(); got != 0 {
					t.Fatalf("progress events=%d after rollback", got)
				}
			},
			assertCommitted: func() {
				if got := count(); got != 1 {
					t.Fatalf("progress events=%d, want 1", got)
				}
			},
			recover: invoke,
		}
	})
}

func TestReportV6WorkProgressRejectsForeignOrSettledAttempt(t *testing.T) {
	run := newTransactionRecoveryRun(t, "Progress fence")
	membershipID, workItemID := seedV6RecoveryWorkItem(t, run, "running", time.Now().Add(time.Minute))
	attemptID := seedV6RecoveryAttempt(t, run, membershipID, workItemID)

	foreign := ReportV6WorkProgressInput{
		V6AttemptAccess: V6AttemptAccess{
			WorkspaceID: run.fixture.workspaceID, RunID: run.fixture.sessionID,
			WorkItemID: workItemID, AttemptID: attemptID, AgentID: uuid.NewString(),
		},
		ClientRequestID: uuid.NewString(), Text: "spoofed",
	}
	if err := run.store.ReportV6WorkProgress(run.ctx, foreign); !errors.Is(err, ErrAttemptNotAssigned) {
		t.Fatalf("foreign agent error=%v, want ErrAttemptNotAssigned", err)
	}

	if _, err := run.pool.Exec(run.ctx, `
		UPDATE research_work_item_attempt SET status='succeeded' WHERE id=$1::uuid
	`, attemptID); err != nil {
		t.Fatal(err)
	}
	settled := ReportV6WorkProgressInput{
		V6AttemptAccess: V6AttemptAccess{
			WorkspaceID: run.fixture.workspaceID, RunID: run.fixture.sessionID,
			WorkItemID: workItemID, AttemptID: attemptID, AgentID: run.fixture.agentID,
		},
		ClientRequestID: uuid.NewString(), Text: "late note",
	}
	if err := run.store.ReportV6WorkProgress(run.ctx, settled); !errors.Is(err, ErrAttemptNotAssigned) {
		t.Fatalf("settled attempt error=%v, want ErrAttemptNotAssigned", err)
	}
}
