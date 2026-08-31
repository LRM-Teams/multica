package service

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

type universalDAGBoundaryFixture struct {
	kind       DAGBoundaryKind
	closeKind  DAGCloseActionKind
	endSeq     int32
	actionKey  string
	derivative bool
}

type universalDAGBoundaryHarness struct {
	pool      *pgxpool.Pool
	conn      *pgxpool.Conn
	schema    string
	workspace pgtype.UUID
	project   pgtype.UUID
	channel   pgtype.UUID
}

func applyUniversalDAGEdgeOnlyLinkageMigration(t *testing.T, ctx context.Context, conn *pgxpool.Conn) {
	t.Helper()
	_, filename, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("locate universal DAG linkage test")
	}
	path := filepath.Join(filepath.Dir(filename), "..", "..", "migrations", "464_universal_dag_edge_only_linkage.up.sql")
	migration, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read migration 464: %v", err)
	}
	if _, err := conn.Exec(ctx, string(migration)); err != nil {
		t.Fatalf("apply migration 464 in private schema: %v", err)
	}
}

func TestUniversalInteractionDAGGenerationStateMachine(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	h := newUniversalDAGBoundaryHarness(t, ctx)
	defer h.Close()

	cases := []struct {
		name         string
		messageCount int
		events       []universalDAGBoundaryFixture
		wantRanges   [][2]int32
		wantKinds    []string
	}{
		{
			name: "batched inbound then output", messageCount: 3,
			events: []universalDAGBoundaryFixture{
				{kind: DAGBoundaryInbound, endSeq: 1},
				{kind: DAGBoundaryInbound, endSeq: 2},
				{kind: DAGBoundaryVisible, closeKind: DAGCloseMessage, endSeq: 3, actionKey: "visible-1"},
			},
			wantRanges: [][2]int32{{1, 3}}, wantKinds: []string{"message"},
		},
		{
			name: "open and close first outbound", messageCount: 1,
			events: []universalDAGBoundaryFixture{
				{kind: DAGBoundaryVisible, closeKind: DAGCloseReaction, endSeq: 1, actionKey: "reaction-1"},
			},
			wantRanges: [][2]int32{{1, 1}}, wantKinds: []string{"reaction"},
		},
		{
			name: "consecutive visible output", messageCount: 2,
			events: []universalDAGBoundaryFixture{
				{kind: DAGBoundaryVisible, closeKind: DAGCloseMessage, endSeq: 1, actionKey: "visible-1"},
				{kind: DAGBoundaryVisible, closeKind: DAGCloseMessage, endSeq: 2, actionKey: "visible-2"},
			},
			wantRanges: [][2]int32{{1, 1}, {2, 2}}, wantKinds: []string{"message", "message"},
		},
		{
			name: "cancel closes open input", messageCount: 1,
			events: []universalDAGBoundaryFixture{
				{kind: DAGBoundaryInbound, endSeq: 1},
				{kind: DAGBoundaryTerminal, closeKind: DAGCloseTerminal, actionKey: "cancelled"},
			},
			wantRanges: [][2]int32{{1, 1}}, wantKinds: []string{"terminal"},
		},
		{
			name: "failed empty task", messageCount: 0,
			events: []universalDAGBoundaryFixture{
				{kind: DAGBoundaryTerminal, closeKind: DAGCloseTerminal, actionKey: "failed"},
			},
			wantRanges: [][2]int32{{0, 0}}, wantKinds: []string{"metadata_only"},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			task := h.createTask(t, ctx, tc.messageCount)
			for _, event := range tc.events {
				in := h.boundaryInput(task, event)
				result, err := h.recordBoundary(ctx, in)
				if err != nil {
					t.Fatalf("record boundary: %v", err)
				}
				if event.kind == DAGBoundaryInbound && result.Closed {
					t.Fatal("inbound boundary unexpectedly closed a generation")
				}
			}

			rows, err := db.New(h.conn).ListUniversalDAGSegmentsByTask(ctx, db.ListUniversalDAGSegmentsByTaskParams{
				WorkspaceID: h.workspace, AgentRunID: task.ID,
			})
			if err != nil {
				t.Fatalf("list task segments: %v", err)
			}
			if len(rows) != len(tc.wantRanges) {
				t.Fatalf("segment count=%d want=%d", len(rows), len(tc.wantRanges))
			}
			for i, row := range rows {
				if row.Generation != int64(i+1) || row.StartSeq != tc.wantRanges[i][0] || row.EndSeq != tc.wantRanges[i][1] {
					t.Fatalf("segment %d generation/range=(%d,%d,%d) want=(%d,%d,%d)", i, row.Generation, row.StartSeq, row.EndSeq, i+1, tc.wantRanges[i][0], tc.wantRanges[i][1])
				}
				if !row.CloseActionKind.Valid || row.CloseActionKind.String != tc.wantKinds[i] {
					t.Fatalf("segment %d close kind=%q want=%q", i, row.CloseActionKind.String, tc.wantKinds[i])
				}
				if row.SegmentID == "" || len(row.SegmentID) != 64 {
					t.Fatalf("segment %d ID is not opaque SHA-256 identity", i)
				}
			}

			var outboxCount int
			if err := h.conn.QueryRow(ctx, `SELECT count(*) FROM interaction_dag_publish_outbox WHERE workspace_id=$1 AND segment_id IN (SELECT segment_id FROM interaction_dag_segment WHERE workspace_id=$1 AND agent_run_id=$2)`, h.workspace, task.ID).Scan(&outboxCount); err != nil {
				t.Fatalf("count outbox: %v", err)
			}
			if outboxCount != len(rows) {
				t.Fatalf("outbox count=%d want=%d", outboxCount, len(rows))
			}
			if len(rows) > 1 {
				var continues int
				if err := h.conn.QueryRow(ctx, `SELECT count(*) FROM interaction_dag_edge WHERE workspace_id=$1 AND type='continues' AND src_segment_id=$2 AND dst_segment_id=$3`, h.workspace, rows[0].SegmentID, rows[1].SegmentID).Scan(&continues); err != nil {
					t.Fatalf("count continues edge: %v", err)
				}
				if continues != 1 {
					t.Fatalf("continues edge count=%d want=1", continues)
				}
			}
		})
	}
}

func TestUniversalInteractionDAGDuplicateBoundaryAndSequenceGap(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	h := newUniversalDAGBoundaryHarness(t, ctx)
	defer h.Close()
	task := h.createTask(t, ctx, 2)

	gap := h.boundaryInput(task, universalDAGBoundaryFixture{kind: DAGBoundaryInbound, endSeq: 2})
	if _, err := h.recordBoundary(ctx, gap); !errors.Is(err, ErrDAGSequenceGap) {
		t.Fatalf("sequence gap error=%v want ErrDAGSequenceGap", err)
	}
	var cursorCount int
	if err := h.conn.QueryRow(ctx, `SELECT count(*) FROM interaction_dag_task_cursor WHERE workspace_id=$1 AND agent_run_id=$2`, h.workspace, task.ID).Scan(&cursorCount); err != nil {
		t.Fatalf("count cursor after gap: %v", err)
	}
	if cursorCount != 0 {
		t.Fatal("rejected sequence gap persisted cursor state")
	}

	in := h.boundaryInput(task, universalDAGBoundaryFixture{kind: DAGBoundaryVisible, closeKind: DAGCloseMessage, endSeq: 1, actionKey: "duplicate-visible"})
	const workers = 2
	results := make(chan DAGBoundaryResult, workers)
	errs := make(chan error, workers)
	var wg sync.WaitGroup
	for range workers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			conn, err := h.pool.Acquire(ctx)
			if err != nil {
				errs <- err
				return
			}
			defer conn.Release()
			if _, err := conn.Exec(ctx, "SET search_path TO "+pgx.Identifier{h.schema}.Sanitize()); err != nil {
				errs <- err
				return
			}
			tx, err := conn.Begin(ctx)
			if err != nil {
				errs <- err
				return
			}
			defer tx.Rollback(ctx)
			result, err := NewUniversalInteractionDAG().RecordBoundaryTx(ctx, db.New(tx), tx, in)
			if err == nil {
				err = tx.Commit(ctx)
			}
			if err != nil {
				errs <- err
				return
			}
			results <- result
		}()
	}
	wg.Wait()
	close(results)
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatalf("concurrent replay: %v", err)
		}
	}
	var first DAGBoundaryResult
	for result := range results {
		if first.SegmentID == "" {
			first = result
			continue
		}
		if result != first {
			t.Fatalf("duplicate boundary result=%+v want=%+v", result, first)
		}
	}
	var segmentCount int
	if err := h.conn.QueryRow(ctx, `SELECT count(*) FROM interaction_dag_segment WHERE workspace_id=$1 AND agent_run_id=$2`, h.workspace, task.ID).Scan(&segmentCount); err != nil {
		t.Fatalf("count replay segments: %v", err)
	}
	if segmentCount != 1 || first.Generation != 1 {
		t.Fatalf("duplicate replay segment count=%d generation=%d", segmentCount, first.Generation)
	}
}

func TestUniversalInteractionDAGRetryChildAndDerivative(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	h := newUniversalDAGBoundaryHarness(t, ctx)
	defer h.Close()
	parent := h.createTask(t, ctx, 1)
	child := h.createTask(t, ctx, 1)

	parentResult, err := h.recordBoundary(ctx, h.boundaryInput(parent, universalDAGBoundaryFixture{kind: DAGBoundaryVisible, closeKind: DAGCloseMessage, endSeq: 1, actionKey: "parent-visible"}))
	if err != nil {
		t.Fatalf("record parent: %v", err)
	}
	childInput := h.boundaryInput(child, universalDAGBoundaryFixture{kind: DAGBoundaryVisible, closeKind: DAGCloseMessage, endSeq: 1, actionKey: "child-visible", derivative: true})
	childResult, err := h.recordBoundary(ctx, childInput)
	if err != nil {
		t.Fatalf("record retry child: %v", err)
	}
	if parentResult.Generation != 1 || childResult.Generation != 1 || parentResult.SegmentID == childResult.SegmentID {
		t.Fatalf("retry child reused parent lifecycle: parent=%+v child=%+v", parentResult, childResult)
	}
	row, err := db.New(h.conn).GetUniversalDAGSegment(ctx, db.GetUniversalDAGSegmentParams{WorkspaceID: h.workspace, SegmentID: childResult.SegmentID})
	if err != nil {
		t.Fatalf("read child segment: %v", err)
	}
	if !row.Derivative || row.GraphProjectionEligibleAtEvent || row.TrainableEligible {
		t.Fatalf("derivative eligibility was not frozen safely: derivative=%t graph=%t trainable=%t", row.Derivative, row.GraphProjectionEligibleAtEvent, row.TrainableEligible)
	}
}

func TestUniversalInteractionDAGLinkage(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	h := newUniversalDAGBoundaryHarness(t, ctx)
	defer h.Close()
	dag := NewUniversalInteractionDAG()

	for _, edgeType := range []string{EdgeTypeRespondsTo, EdgeTypeDelegation, EdgeTypeMention} {
		t.Run(edgeType, func(t *testing.T) {
			sourceTask := h.createTask(t, ctx, 1)
			targetTask := h.createTask(t, ctx, 1)
			source, err := h.recordBoundary(ctx, h.boundaryInput(sourceTask, universalDAGBoundaryFixture{kind: DAGBoundaryVisible, closeKind: DAGCloseMessage, endSeq: 1, actionKey: edgeType + "-source"}))
			if err != nil {
				t.Fatalf("record source: %v", err)
			}
			if _, err := h.recordBoundary(ctx, h.boundaryInput(targetTask, universalDAGBoundaryFixture{kind: DAGBoundaryVisible, closeKind: DAGCloseMessage, endSeq: 1, actionKey: edgeType + "-target"})); err != nil {
				t.Fatalf("record target: %v", err)
			}
			var durableEventID pgtype.UUID
			if err := h.conn.QueryRow(ctx, `SELECT id FROM task_message WHERE task_id=$1 AND seq=1`, sourceTask.ID).Scan(&durableEventID); err != nil {
				t.Fatalf("read durable event ID: %v", err)
			}
			input := DAGLinkageInput{WorkspaceID: h.workspace, SourceSegmentID: source.SegmentID, TargetRunID: targetTask.ID, Type: edgeType, DurableEventID: durableEventID}
			for range 2 {
				tx, err := h.conn.Begin(ctx)
				if err != nil {
					t.Fatalf("begin linkage: %v", err)
				}
				if err := dag.RecordLinkageTx(ctx, db.New(tx), tx, input); err != nil {
					tx.Rollback(ctx)
					t.Fatalf("record linkage: %v", err)
				}
				if err := tx.Commit(ctx); err != nil {
					t.Fatalf("commit linkage: %v", err)
				}
			}
			var count int
			if err := h.conn.QueryRow(ctx, `SELECT count(*) FROM interaction_dag_edge WHERE workspace_id=$1 AND src_segment_id=$2 AND type=$3 AND trigger_message_id=$4`, h.workspace, source.SegmentID, edgeType, durableEventID).Scan(&count); err != nil {
				t.Fatalf("count linkage: %v", err)
			}
			if count != 1 {
				t.Fatalf("idempotent linkage count=%d want=1", count)
			}
		})
	}

	t.Run(EdgeTypeContinues, func(t *testing.T) {
		task := h.createTask(t, ctx, 2)
		first, err := h.recordBoundary(ctx, h.boundaryInput(task, universalDAGBoundaryFixture{kind: DAGBoundaryVisible, closeKind: DAGCloseMessage, endSeq: 1, actionKey: "continues-source"}))
		if err != nil {
			t.Fatalf("record first generation: %v", err)
		}
		second, err := h.recordBoundary(ctx, h.boundaryInput(task, universalDAGBoundaryFixture{kind: DAGBoundaryVisible, closeKind: DAGCloseMessage, endSeq: 2, actionKey: "continues-target"}))
		if err != nil {
			t.Fatalf("record second generation: %v", err)
		}
		input := DAGLinkageInput{WorkspaceID: h.workspace, SourceSegmentID: first.SegmentID, TargetRunID: task.ID, Type: EdgeTypeContinues}
		tx, err := h.conn.Begin(ctx)
		if err != nil {
			t.Fatalf("begin continues replay: %v", err)
		}
		if err := dag.RecordLinkageTx(ctx, db.New(tx), tx, input); err != nil {
			tx.Rollback(ctx)
			t.Fatalf("record continues replay: %v", err)
		}
		if err := tx.Commit(ctx); err != nil {
			t.Fatalf("commit continues replay: %v", err)
		}
		var count, total int
		if err := h.conn.QueryRow(ctx, `
			SELECT
			  count(*) FILTER (WHERE dst_segment_id=$3),
			  count(*)
			FROM interaction_dag_edge
			WHERE workspace_id=$1 AND src_segment_id=$2 AND type='continues'
		`, h.workspace, first.SegmentID, second.SegmentID).Scan(&count, &total); err != nil {
			t.Fatalf("count continues linkage: %v", err)
		}
		if count != 1 || total != 1 {
			t.Fatalf("continues linkage target count=%d total=%d want=1/1", count, total)
		}

		otherTask := h.createTask(t, ctx, 1)
		if _, err := h.recordBoundary(ctx, h.boundaryInput(otherTask, universalDAGBoundaryFixture{kind: DAGBoundaryVisible, closeKind: DAGCloseMessage, endSeq: 1, actionKey: "forged-continues-target"})); err != nil {
			t.Fatalf("record forged target: %v", err)
		}
		forged := DAGLinkageInput{WorkspaceID: h.workspace, SourceSegmentID: first.SegmentID, TargetRunID: otherTask.ID, Type: EdgeTypeContinues}
		tx, err = h.conn.Begin(ctx)
		if err != nil {
			t.Fatalf("begin forged continues: %v", err)
		}
		defer tx.Rollback(ctx)
		if err := dag.RecordLinkageTx(ctx, db.New(tx), tx, forged); err == nil {
			t.Fatal("cross-task continues linkage unexpectedly succeeded")
		}
	})
}

func TestUniversalInteractionDAGLinkageCloseKindMatrix(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	h := newUniversalDAGBoundaryHarness(t, ctx)
	defer h.Close()
	dag := NewUniversalInteractionDAG()

	recordLinkage := func(t *testing.T, input DAGLinkageInput, wantErr bool) {
		t.Helper()
		tx, err := h.conn.Begin(ctx)
		if err != nil {
			t.Fatalf("begin linkage: %v", err)
		}
		err = dag.RecordLinkageTx(ctx, db.New(tx), tx, input)
		if wantErr {
			if err == nil {
				tx.Rollback(ctx)
				t.Fatal("contradictory linkage unexpectedly succeeded")
			}
			if rollbackErr := tx.Rollback(ctx); rollbackErr != nil {
				t.Fatalf("rollback rejected linkage: %v", rollbackErr)
			}
			return
		}
		if err != nil {
			tx.Rollback(ctx)
			t.Fatalf("record linkage: %v", err)
		}
		if err := tx.Commit(ctx); err != nil {
			t.Fatalf("commit linkage: %v", err)
		}
	}

	targetTask := h.createTask(t, ctx, 1)
	if _, err := h.recordBoundary(ctx, h.boundaryInput(targetTask, universalDAGBoundaryFixture{
		kind: DAGBoundaryVisible, closeKind: DAGCloseMessage, endSeq: 1, actionKey: "matrix-target",
	})); err != nil {
		t.Fatalf("record target anchor: %v", err)
	}

	reactionTask := h.createTask(t, ctx, 1)
	reaction, err := h.recordBoundary(ctx, h.boundaryInput(reactionTask, universalDAGBoundaryFixture{
		kind: DAGBoundaryVisible, closeKind: DAGCloseReaction, endSeq: 1, actionKey: "matrix-reaction",
	}))
	if err != nil {
		t.Fatalf("record reaction source: %v", err)
	}
	var reactionEventID pgtype.UUID
	if err := h.conn.QueryRow(ctx, `SELECT id FROM task_message WHERE task_id=$1 AND seq=1`, reactionTask.ID).Scan(&reactionEventID); err != nil {
		t.Fatalf("read reaction source event: %v", err)
	}
	for _, edgeType := range []string{EdgeTypeDelegation, EdgeTypeMention} {
		t.Run("reject-reaction-"+edgeType, func(t *testing.T) {
			recordLinkage(t, DAGLinkageInput{
				WorkspaceID: h.workspace, SourceSegmentID: reaction.SegmentID,
				TargetRunID: targetTask.ID, Type: edgeType, DurableEventID: reactionEventID,
			}, true)
		})
	}

	messageTask := h.createTask(t, ctx, 1)
	message, err := h.recordBoundary(ctx, h.boundaryInput(messageTask, universalDAGBoundaryFixture{
		kind: DAGBoundaryVisible, closeKind: DAGCloseMessage, endSeq: 1, actionKey: "matrix-message",
	}))
	if err != nil {
		t.Fatalf("record message source: %v", err)
	}
	recordLinkage(t, DAGLinkageInput{
		WorkspaceID: h.workspace, SourceSegmentID: message.SegmentID,
		TargetRunID: targetTask.ID, Type: EdgeTypeRespondsTo,
	}, true)

	edgeOnlyTask := h.createTask(t, ctx, 0)
	edgeOnly, err := h.recordBoundary(ctx, h.boundaryInput(edgeOnlyTask, universalDAGBoundaryFixture{
		kind: DAGBoundaryTerminal, closeKind: DAGCloseMetadataOnly, endSeq: 0,
	}))
	if err != nil {
		t.Fatalf("record edge-only source: %v", err)
	}
	for _, edgeType := range []string{EdgeTypeRespondsTo, EdgeTypeDelegation, EdgeTypeMention} {
		t.Run("edge-only-"+edgeType, func(t *testing.T) {
			input := DAGLinkageInput{
				WorkspaceID: h.workspace, SourceSegmentID: edgeOnly.SegmentID,
				TargetRunID: targetTask.ID, Type: edgeType,
			}
			recordLinkage(t, input, false)
			recordLinkage(t, input, false)
			var count int
			if err := h.conn.QueryRow(ctx, `
				SELECT count(*) FROM interaction_dag_edge
				WHERE workspace_id=$1 AND src_segment_id=$2 AND type=$3
				  AND trigger_message_id IS NULL
			`, h.workspace, edgeOnly.SegmentID, edgeType).Scan(&count); err != nil {
				t.Fatalf("count edge-only linkage: %v", err)
			}
			if count != 1 {
				t.Fatalf("edge-only linkage count=%d want=1", count)
			}
		})
	}
}

func TestUniversalInteractionDAGProviderCapturePostconditions(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	h := newUniversalDAGBoundaryHarness(t, ctx)
	defer h.Close()
	dag := NewUniversalInteractionDAG()
	runID := mustUUID(t, universalRunA)
	runAgentID := mustUUID(t, universalRunAgentA)

	assertNotExpected := func(t *testing.T, input DAGBoundaryInput) DAGBoundaryResult {
		t.Helper()
		result, err := h.recordBoundary(ctx, input)
		if err != nil {
			t.Fatalf("record not-expected capture boundary: %v", err)
		}
		var status string
		if err := h.conn.QueryRow(ctx, `SELECT provider_capture_status FROM interaction_dag_segment WHERE segment_id=$1`, result.SegmentID).Scan(&status); err != nil {
			t.Fatalf("read not-expected capture status: %v", err)
		}
		if status != "not_expected" {
			t.Fatalf("capture status=%q want=not_expected", status)
		}
		return result
	}

	flagOnlyTask := h.createTask(t, ctx, 1)
	flagOnlyInput := h.boundaryInput(flagOnlyTask, universalDAGBoundaryFixture{
		kind: DAGBoundaryVisible, closeKind: DAGCloseMessage, endSeq: 1, actionKey: "capture-flag-only",
	})
	flagOnlyInput.RunID, flagOnlyInput.RunAgentID = runID, runAgentID
	flagOnlyInput.ProviderCaptureExpected = true
	flagOnly := assertNotExpected(t, flagOnlyInput)

	untrustedTask := h.createTask(t, ctx, 1)
	untrustedInput := h.boundaryInput(untrustedTask, universalDAGBoundaryFixture{
		kind: DAGBoundaryVisible, closeKind: DAGCloseMessage, endSeq: 1, actionKey: "capture-untrusted",
	})
	untrustedInput.RunID, untrustedInput.RunAgentID = runID, runAgentID
	untrusted := assertNotExpected(t, untrustedInput)

	owner := ProviderCallAssociation{
		ProviderCallID: "call-a-1", Role: "owned", Ordinal: 1,
		RunID: runID, RunAgentID: runAgentID, CaptureVersion: 1,
		CorrelationKey: "capture-not-expected",
	}
	for _, segmentID := range []string{flagOnly.SegmentID, untrusted.SegmentID} {
		tx, err := h.conn.Begin(ctx)
		if err != nil {
			t.Fatalf("begin rejected capture attachment: %v", err)
		}
		err = dag.AttachProviderCaptureTx(ctx, db.New(tx), tx, segmentID, "capture-rejected", []ProviderCallAssociation{owner})
		if err == nil || err.Error() != "provider capture was not expected for this segment" {
			tx.Rollback(ctx)
			t.Fatalf("not-expected capture attachment error=%v", err)
		}
		if err := tx.Rollback(ctx); err != nil {
			t.Fatalf("rollback rejected capture attachment: %v", err)
		}
	}

	incompleteTask := h.createTask(t, ctx, 1)
	incompleteInput := h.boundaryInput(incompleteTask, universalDAGBoundaryFixture{
		kind: DAGBoundaryVisible, closeKind: DAGCloseMessage, endSeq: 1, actionKey: "capture-incomplete",
	})
	incompleteInput.ProviderCaptureExpected = true
	incompleteInput.ProviderCaptureCorrelationKey = "capture-incomplete"
	if _, err := h.recordBoundary(ctx, incompleteInput); err == nil {
		t.Fatal("pending capture unexpectedly accepted incomplete run identity")
	}

	pendingTask := h.createTask(t, ctx, 1)
	pendingInput := h.boundaryInput(pendingTask, universalDAGBoundaryFixture{
		kind: DAGBoundaryVisible, closeKind: DAGCloseMessage, endSeq: 1, actionKey: "capture-pending",
	})
	pendingInput.RunID, pendingInput.RunAgentID = runID, runAgentID
	pendingInput.ProviderCaptureExpected = true
	pendingInput.ProviderCaptureCorrelationKey = "capture-pending"
	pending, err := h.recordBoundary(ctx, pendingInput)
	if err != nil {
		t.Fatalf("record pending capture boundary: %v", err)
	}

	type immutableCaptureState struct {
		StartSeq          int32
		EndSeq            int32
		CloseActionKind   string
		CanonicalActionID string
		OutboxSegmentID   string
		RequestHash       string
	}
	readState := func(t *testing.T) (immutableCaptureState, string) {
		t.Helper()
		var state immutableCaptureState
		var status string
		if err := h.conn.QueryRow(ctx, `
			SELECT segment.start_seq, segment.end_seq, segment.close_action_kind,
			       segment.canonical_action_id::text, outbox.segment_id,
			       outbox.request_hash, segment.provider_capture_status
			FROM interaction_dag_segment AS segment
			JOIN interaction_dag_publish_outbox AS outbox
			  ON outbox.workspace_id=segment.workspace_id
			 AND outbox.segment_id=segment.segment_id
			WHERE segment.segment_id=$1
		`, pending.SegmentID).Scan(
			&state.StartSeq, &state.EndSeq, &state.CloseActionKind,
			&state.CanonicalActionID, &state.OutboxSegmentID,
			&state.RequestHash, &status,
		); err != nil {
			t.Fatalf("read immutable capture state: %v", err)
		}
		return state, status
	}
	before, beforeStatus := readState(t)
	if beforeStatus != "pending" {
		t.Fatalf("capture status before attachment=%q want=pending", beforeStatus)
	}
	pendingOwner := owner
	pendingOwner.CorrelationKey = "capture-pending"
	h.attachCapture(t, ctx, dag, pending.SegmentID, "capture-final", []ProviderCallAssociation{pendingOwner}, false)
	after, afterStatus := readState(t)
	if afterStatus != "finalized" {
		t.Fatalf("capture status after attachment=%q want=finalized", afterStatus)
	}
	if before != after {
		t.Fatalf("capture attachment changed immutable state: before=%+v after=%+v", before, after)
	}
}

func TestFinalizeTerminalTaskSideEffectsUniversalDAGProjectLookupFailure(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	h := newUniversalDAGBoundaryHarness(t, ctx)
	defer h.Close()

	taskID := pgtype.UUID{Bytes: uuid.New(), Valid: true}
	if _, err := h.conn.Exec(ctx, `INSERT INTO agent_inbox_event(id,workspace_id) VALUES ($1,$2)`, taskID, h.workspace); err != nil {
		t.Fatalf("insert terminal task: %v", err)
	}
	task := db.AgentInboxEvent{
		ID: taskID, WorkspaceID: h.workspace,
		ChatSessionID: pgtype.UUID{Bytes: uuid.New(), Valid: true},
	}
	svc := NewTaskService(db.New(h.conn), h.conn, nil, nil)
	svc.FinalizeTerminalTaskSideEffects(ctx, task)

	rows, err := db.New(h.conn).ListUniversalDAGSegmentsByTask(ctx, db.ListUniversalDAGSegmentsByTaskParams{
		WorkspaceID: h.workspace, AgentRunID: taskID,
	})
	if err != nil {
		t.Fatalf("list terminal lifecycle after project lookup failure: %v", err)
	}
	if len(rows) != 1 || rows[0].StartSeq != 0 || rows[0].EndSeq != 0 ||
		rows[0].CloseActionKind.String != string(DAGCloseMetadataOnly) ||
		rows[0].ProjectIDAtEvent.Valid || rows[0].ChannelIDAtEvent.Valid {
		t.Fatalf("terminal lifecycle after project lookup failure=%+v", rows)
	}
}

func TestUniversalInteractionDAGProviderCapture(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	h := newUniversalDAGBoundaryHarness(t, ctx)
	defer h.Close()
	dag := NewUniversalInteractionDAG()
	runID := mustUUID(t, universalRunA)
	runAgentID := mustUUID(t, universalRunAgentA)

	// Use the canonical Task 1 provider owners for the association constraints.
	providerTask := db.AgentInboxEvent{ID: mustUUID(t, universalTaskA), WorkspaceID: h.workspace, IssueID: pgtype.UUID{}, ChannelID: h.channel}
	firstInput := h.boundaryInput(providerTask, universalDAGBoundaryFixture{kind: DAGBoundaryVisible, closeKind: DAGCloseMessage, endSeq: 1, actionKey: "provider-owner"})
	firstInput.RunID, firstInput.RunAgentID = runID, runAgentID
	firstInput.ProviderCaptureExpected = true
	firstInput.ProviderCaptureCorrelationKey = "capture-correlation-1"
	first, err := h.recordBoundary(ctx, firstInput)
	if err != nil {
		t.Fatalf("record provider owner segment: %v", err)
	}
	owner := ProviderCallAssociation{ProviderCallID: "call-a-1", Role: "owned", Ordinal: 1, RunID: runID, RunAgentID: runAgentID, CaptureVersion: 1, CorrelationKey: "capture-correlation-1"}
	h.attachCapture(t, ctx, dag, first.SegmentID, "capture-1", []ProviderCallAssociation{owner}, false)
	h.attachCapture(t, ctx, dag, first.SegmentID, "capture-1", []ProviderCallAssociation{owner}, false)

	secondInput := h.boundaryInput(providerTask, universalDAGBoundaryFixture{kind: DAGBoundaryVisible, closeKind: DAGCloseMessage, endSeq: 2, actionKey: "provider-shared"})
	secondInput.RunID, secondInput.RunAgentID = runID, runAgentID
	secondInput.ProviderCaptureExpected = true
	secondInput.ProviderCaptureCorrelationKey = "capture-correlation-2"
	second, err := h.recordBoundary(ctx, secondInput)
	if err != nil {
		t.Fatalf("record provider shared segment: %v", err)
	}
	calls := []ProviderCallAssociation{
		{ProviderCallID: "call-a-1", Role: "shared_producer", Ordinal: 1, RunID: runID, RunAgentID: runAgentID, CaptureVersion: 2, CorrelationKey: "capture-correlation-2"},
		{ProviderCallID: "call-a-2", Role: "audit", Ordinal: 2, RunID: runID, RunAgentID: runAgentID, CaptureVersion: 2, CorrelationKey: "capture-correlation-2"},
	}
	h.attachCapture(t, ctx, dag, second.SegmentID, "capture-2", calls, false)

	var finalized, associationCount int
	if err := h.conn.QueryRow(ctx, `SELECT count(*) FILTER (WHERE provider_capture_status='finalized'), (SELECT count(*) FROM interaction_dag_universal_provider_call WHERE segment_id IN ($1,$2)) FROM interaction_dag_segment WHERE segment_id IN ($1,$2)`, first.SegmentID, second.SegmentID).Scan(&finalized, &associationCount); err != nil {
		t.Fatalf("read capture state: %v", err)
	}
	if finalized != 2 || associationCount != 3 {
		t.Fatalf("capture finalized=%d associations=%d", finalized, associationCount)
	}

	conflict := append([]ProviderCallAssociation(nil), calls...)
	for i := range conflict {
		conflict[i].CorrelationKey = "wrong-correlation"
	}
	h.attachCapture(t, ctx, dag, second.SegmentID, "capture-conflict", conflict, true)
	var status, captureID string
	if err := h.conn.QueryRow(ctx, `SELECT provider_capture_status,provider_capture_id FROM interaction_dag_segment WHERE segment_id=$1`, second.SegmentID).Scan(&status, &captureID); err != nil {
		t.Fatalf("read conflict state: %v", err)
	}
	if status != "conflict" || captureID != "capture-2" {
		t.Fatalf("conflict state=%q capture=%q", status, captureID)
	}
}

func TestFinalizeTerminalTaskSideEffectsUniversalDAGUnscoped(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	h := newUniversalDAGBoundaryHarness(t, ctx)
	defer h.Close()

	taskID := pgtype.UUID{Bytes: uuid.New(), Valid: true}
	if _, err := h.conn.Exec(ctx, `INSERT INTO agent_inbox_event(id,workspace_id) VALUES ($1,$2)`, taskID, h.workspace); err != nil {
		t.Fatalf("insert unscoped task: %v", err)
	}
	task := db.AgentInboxEvent{ID: taskID, WorkspaceID: h.workspace}
	svc := NewTaskService(db.New(h.conn), h.conn, nil, nil)
	svc.FinalizeTerminalTaskSideEffects(ctx, task)
	svc.FinalizeTerminalTaskSideEffects(ctx, task)

	rows, err := db.New(h.conn).ListUniversalDAGSegmentsByTask(ctx, db.ListUniversalDAGSegmentsByTaskParams{
		WorkspaceID: h.workspace, AgentRunID: taskID,
	})
	if err != nil {
		t.Fatalf("list unscoped terminal lifecycle: %v", err)
	}
	if len(rows) != 1 || rows[0].StartSeq != 0 || rows[0].EndSeq != 0 ||
		rows[0].CloseActionKind.String != string(DAGCloseMetadataOnly) ||
		rows[0].ProjectIDAtEvent.Valid || rows[0].ChannelIDAtEvent.Valid {
		t.Fatalf("unexpected unscoped terminal lifecycle metadata")
	}
}

func (h *universalDAGBoundaryHarness) attachCapture(t *testing.T, ctx context.Context, dag *UniversalInteractionDAG, segmentID, captureID string, calls []ProviderCallAssociation, wantConflict bool) {
	t.Helper()
	tx, err := h.conn.Begin(ctx)
	if err != nil {
		t.Fatalf("begin provider attach: %v", err)
	}
	defer tx.Rollback(ctx)
	err = dag.AttachProviderCaptureTx(ctx, db.New(tx), tx, segmentID, captureID, calls)
	if wantConflict != errors.Is(err, ErrDAGProviderCaptureConflict) {
		t.Fatalf("provider attach conflict=%t error=%v", wantConflict, err)
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatalf("commit provider attach: %v", err)
	}
}

func newUniversalDAGBoundaryHarness(t *testing.T, ctx context.Context) *universalDAGBoundaryHarness {
	t.Helper()
	pool, conn := openUniversalDAGServiceSchema(t, ctx)
	if _, err := conn.Exec(ctx, universalDAGLegacySchema); err != nil {
		conn.Release()
		t.Fatalf("create pre-454 schema: %v", err)
	}
	applyUniversalDAGMigrationIfPresent(t, ctx, conn)
	applyUniversalDAGEdgeOnlyLinkageMigration(t, ctx, conn)
	seedUniversalDAGCanonicalOwners(t, ctx, conn)
	var schema string
	if err := conn.QueryRow(ctx, `SELECT current_schema()`).Scan(&schema); err != nil {
		conn.Release()
		t.Fatalf("read private schema: %v", err)
	}
	return &universalDAGBoundaryHarness{
		pool: pool, conn: conn, schema: schema,
		workspace: mustUUID(t, universalWSA), project: mustUUID(t, universalProjectA), channel: mustUUID(t, universalChannelA),
	}
}

func (h *universalDAGBoundaryHarness) createTask(t *testing.T, ctx context.Context, messageCount int) db.AgentInboxEvent {
	t.Helper()
	taskID := pgtype.UUID{Bytes: uuid.New(), Valid: true}
	if _, err := h.conn.Exec(ctx, `INSERT INTO agent_inbox_event(id,workspace_id,channel_id) VALUES ($1,$2,$3)`, taskID, h.workspace, h.channel); err != nil {
		t.Fatalf("insert task: %v", err)
	}
	for seq := 1; seq <= messageCount; seq++ {
		if _, err := h.conn.Exec(ctx, `INSERT INTO task_message(task_id,seq,content) VALUES ($1,$2,'')`, taskID, seq); err != nil {
			t.Fatalf("insert canonical task message %d: %v", seq, err)
		}
	}
	return db.AgentInboxEvent{ID: taskID, WorkspaceID: h.workspace, ChannelID: h.channel}
}

func (h *universalDAGBoundaryHarness) boundaryInput(task db.AgentInboxEvent, event universalDAGBoundaryFixture) DAGBoundaryInput {
	actionKey := event.actionKey
	if actionKey != "" {
		actionKey = task.ID.String() + ":" + actionKey
	}
	input := DAGBoundaryInput{
		WorkspaceID:       h.workspace,
		Task:              task,
		BoundaryKind:      event.kind,
		CloseActionKind:   event.closeKind,
		EndSeq:            event.endSeq,
		ActionKey:         actionKey,
		ProjectID:         h.project,
		ChannelID:         h.channel,
		RouteGeneration:   1,
		MemoryTypeAtEvent: "graph",
		Derivative:        event.derivative,
	}
	if event.closeKind == DAGCloseMessage || event.closeKind == DAGCloseReaction {
		input.ActionID = pgtype.UUID{Bytes: uuid.New(), Valid: true}
	}
	return input
}

func (h *universalDAGBoundaryHarness) recordBoundary(ctx context.Context, input DAGBoundaryInput) (DAGBoundaryResult, error) {
	tx, err := h.conn.Begin(ctx)
	if err != nil {
		return DAGBoundaryResult{}, err
	}
	defer tx.Rollback(ctx)
	result, err := NewUniversalInteractionDAG().RecordBoundaryTx(ctx, db.New(tx), tx, input)
	if err != nil {
		return DAGBoundaryResult{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return DAGBoundaryResult{}, err
	}
	return result, nil
}

func (h *universalDAGBoundaryHarness) Close() {
	h.conn.Release()
}

func (f universalDAGBoundaryFixture) String() string {
	return fmt.Sprintf("%s/%s/%d", f.kind, f.closeKind, f.endSeq)
}

func TestUniversalInteractionDAGSegmentOutboxAtomicity(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	h := newUniversalDAGBoundaryHarness(t, ctx)
	defer h.Close()
	task := h.createTask(t, ctx, 1)

	if _, err := h.conn.Exec(ctx, `
		CREATE FUNCTION reject_task2_outbox() RETURNS trigger LANGUAGE plpgsql AS $$
		BEGIN RAISE EXCEPTION 'task2 outbox rejection'; END $$;
		CREATE TRIGGER reject_task2_outbox BEFORE INSERT ON interaction_dag_publish_outbox
		FOR EACH ROW EXECUTE FUNCTION reject_task2_outbox();
	`); err != nil {
		t.Fatalf("install private outbox rejection trigger: %v", err)
	}
	input := h.boundaryInput(task, universalDAGBoundaryFixture{
		kind: DAGBoundaryVisible, closeKind: DAGCloseMessage, endSeq: 1, actionKey: "atomicity",
	})
	if _, err := h.recordBoundary(ctx, input); err == nil {
		t.Fatal("boundary unexpectedly succeeded when outbox insertion failed")
	}
	if _, err := h.conn.Exec(ctx, `DROP TRIGGER reject_task2_outbox ON interaction_dag_publish_outbox; DROP FUNCTION reject_task2_outbox()`); err != nil {
		t.Fatalf("remove private outbox rejection trigger: %v", err)
	}

	var segmentCount, cursorCount, outboxCount int
	if err := h.conn.QueryRow(ctx, `
		SELECT
		  (SELECT count(*) FROM interaction_dag_segment WHERE workspace_id=$1 AND agent_run_id=$2),
		  (SELECT count(*) FROM interaction_dag_task_cursor WHERE workspace_id=$1 AND agent_run_id=$2),
		  (SELECT count(*) FROM interaction_dag_publish_outbox WHERE workspace_id=$1)
	`, h.workspace, task.ID).Scan(&segmentCount, &cursorCount, &outboxCount); err != nil {
		t.Fatalf("read rollback state: %v", err)
	}
	if segmentCount != 0 || cursorCount != 0 || outboxCount != 0 {
		t.Fatalf("failed boundary persisted state: segments=%d cursors=%d outbox=%d", segmentCount, cursorCount, outboxCount)
	}
}
