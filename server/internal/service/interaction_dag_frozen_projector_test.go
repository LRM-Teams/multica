package service

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/multica-ai/multica/server/internal/util"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

// The frozen-projector tests run against one full faithful migration chain
// (including migration 465) inside a private schema, because the projection
// spans the 315 frozen-snapshot tables and the 454/464 canonical store at
// once. The schema is bootstrapped once per test binary through the same
// cmd/migrate entry point production uses.
var (
	universalDAGProjectionOnce sync.Once
	universalDAGProjectionPool *pgxpool.Pool
	universalDAGProjectionDrop func()
	universalDAGProjectionErr  error
)

func TestMain(m *testing.M) {
	code := m.Run()
	if universalDAGProjectionDrop != nil {
		universalDAGProjectionDrop()
	}
	os.Exit(code)
}

// dropProjectionSchema tears the faithful schema down on a dedicated fresh
// connection, dropping relations one statement at a time first: a single
// cascading DROP over a fully migrated schema exceeds this database's
// per-backend lock budget, while per-table drops each need only a handful of
// locks and the final schema drop is then trivially small.
func dropProjectionSchema(databaseURL, schema, quoted string) {
	dropCtx, dropCancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer dropCancel()
	conn, err := pgx.Connect(dropCtx, databaseURL)
	if err != nil {
		fmt.Printf("connect for projection schema drop: %v\n", err)
		return
	}
	defer conn.Close(dropCtx)
	rows, err := conn.Query(dropCtx, `
SELECT quote_ident(relname) FROM pg_class
JOIN pg_namespace ON pg_namespace.oid = relnamespace
WHERE nspname = $1 AND relkind IN ('r','p')`, schema)
	if err == nil {
		var names []string
		for rows.Next() {
			var name string
			if err := rows.Scan(&name); err == nil {
				names = append(names, name)
			}
		}
		rows.Close()
		for _, name := range names {
			if _, err := conn.Exec(dropCtx, "DROP TABLE IF EXISTS "+quoted+"."+name+" CASCADE"); err != nil {
				fmt.Printf("drop projection table %s: %v\n", name, err)
			}
		}
	} else {
		fmt.Printf("list projection tables for drop: %v\n", err)
	}
	if _, err := conn.Exec(dropCtx, "DROP SCHEMA IF EXISTS "+quoted+" CASCADE"); err != nil {
		fmt.Printf("drop projection test schema: %v\n", err)
	}
}

func bootstrapUniversalDAGProjectionSchema(t *testing.T) *pgxpool.Pool {
	t.Helper()
	universalDAGProjectionOnce.Do(func() {
		databaseURL := strings.TrimSpace(os.Getenv("DATABASE_URL"))
		if databaseURL == "" {
			databaseURL = "postgres://multica:multica@localhost:5432/multica?sslmode=disable"
		}
		separator := "?"
		if strings.Contains(databaseURL, "?") {
			separator = "&"
		}
		admin, err := pgxpool.New(context.Background(), databaseURL)
		if err != nil {
			universalDAGProjectionErr = err
			return
		}
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
		defer cancel()
		conn, err := admin.Acquire(ctx)
		if err != nil {
			admin.Close()
			universalDAGProjectionErr = err
			return
		}
		schema := fmt.Sprintf("universal_dag_projection_test_%d", time.Now().UnixNano())
		quoted := pgx.Identifier{schema}.Sanitize()
		if _, err := conn.Exec(ctx, "CREATE SCHEMA "+quoted); err != nil {
			conn.Release()
			admin.Close()
			universalDAGProjectionErr = err
			return
		}
		// Migration 314 aborts when public is on the search path because the
		// dev database carries pre-existing fixture residue there; private
		// dummies keep 314 resolving locally, matching the handler bootstrap.
		for _, fn := range []string{"test_agent_inbox_fixture_defaults", "test_server_agent_inbox_fixture_defaults"} {
			if _, err := conn.Exec(ctx, fmt.Sprintf(
				"CREATE FUNCTION %s.%s() RETURNS void LANGUAGE sql AS 'SELECT NULL'", quoted, fn)); err != nil {
				conn.Release()
				admin.Close()
				universalDAGProjectionErr = err
				return
			}
		}
		conn.Release()

		cmd := exec.CommandContext(ctx, "go", "run", "./cmd/migrate", "up")
		cmd.Dir = universalDAGProjectionModuleDir(t)
		cmd.Env = append(os.Environ(), "DATABASE_URL="+databaseURL+separator+"search_path="+schema+",public")
		if output, err := cmd.CombinedOutput(); err != nil {
			admin.Close()
			universalDAGProjectionErr = fmt.Errorf("apply migrations: %w: %s", err, output)
			return
		}

		scoped, err := pgxpool.New(context.Background(), databaseURL+separator+"search_path="+schema+",public")
		if err != nil {
			admin.Close()
			universalDAGProjectionErr = err
			return
		}
		admin.Close()
		universalDAGProjectionPool = scoped
		universalDAGProjectionDrop = func() {
			scoped.Close()
			dropProjectionSchema(databaseURL, schema, quoted)
		}
	})
	if universalDAGProjectionPool == nil {
		t.Fatalf("bootstrap faithful projection schema: %v", universalDAGProjectionErr)
	}
	return universalDAGProjectionPool
}

func universalDAGProjectionModuleDir(t *testing.T) string {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatalf("determine projection test working directory: %v", err)
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatal("find server module root from projection test working directory")
		}
		dir = parent
	}
}

type universalDAGProjectionHarness struct {
	t         *testing.T
	ctx       context.Context
	pool      *pgxpool.Pool
	workspace pgtype.UUID
	project   pgtype.UUID
	agent     pgtype.UUID
}

func newUniversalDAGProjectionHarness(t *testing.T) *universalDAGProjectionHarness {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	t.Cleanup(cancel)
	pool := bootstrapUniversalDAGProjectionSchema(t)
	h := &universalDAGProjectionHarness{t: t, ctx: ctx, pool: pool}
	h.workspace = projectionFixtureUUID(h, 0x01, time.Now().UnixNano())
	h.exec(`INSERT INTO workspace(id,name,slug) VALUES ($1,'projection-test',$2)`, h.workspace,
		fmt.Sprintf("projection-test-%d", time.Now().UnixNano()))
	h.project = projectionFixtureUUID(h, 0x02, time.Now().UnixNano())
	h.exec(`INSERT INTO project(id,workspace_id,title) VALUES ($1,$2,'projection-test')`, h.project, h.workspace)
	runtimeID := projectionFixtureUUID(h, 0x03, time.Now().UnixNano())
	h.exec(`INSERT INTO agent_runtime(id,workspace_id,name,runtime_mode,provider) VALUES ($1,$2,'projection-runtime','local','synthetic')`, runtimeID, h.workspace)
	h.agent = projectionFixtureUUID(h, 0x04, time.Now().UnixNano())
	h.exec(`INSERT INTO agent(id,workspace_id,name,avatar_url,runtime_mode,runtime_id) VALUES ($1,$2,'projection-agent','','local',$3)`, h.agent, h.workspace, runtimeID)
	return h
}

func projectionFixtureUUID(h *universalDAGProjectionHarness, prefix byte, value int64) pgtype.UUID {
	_ = prefix
	_ = value
	return pgtype.UUID{Bytes: uuid.New(), Valid: true}
}

func (h *universalDAGProjectionHarness) exec(sql string, args ...any) {
	h.t.Helper()
	_, err := h.pool.Exec(h.ctx, sql, args...)
	require.NoError(h.t, err)
}

func (h *universalDAGProjectionHarness) queries() *db.Queries { return db.New(h.pool) }

func (h *universalDAGProjectionHarness) createTask(reason string) db.AgentInboxEvent {
	h.t.Helper()
	taskID := projectionFixtureUUID(h, 0x05, time.Now().UnixNano())
	h.exec(`INSERT INTO agent_inbox_event(id,workspace_id,agent_id,reason) VALUES ($1,$2,$3,$4)`,
		taskID, h.workspace, h.agent, reason)
	return db.AgentInboxEvent{ID: taskID, WorkspaceID: h.workspace}
}

func (h *universalDAGProjectionHarness) addTaskMessage(task db.AgentInboxEvent, seq int32) {
	h.t.Helper()
	h.exec(`INSERT INTO task_message(task_id,seq,type,content) VALUES ($1,$2,'message','')`, task.ID, seq)
}

// createRun provisions one env-dispatch run on its own project (runs are
// one-per-project) and leaves it in running state.
func (h *universalDAGProjectionHarness) createRun() EnvDispatchRunRecord {
	h.t.Helper()
	project := projectionFixtureUUID(h, 0x07, time.Now().UnixNano())
	h.exec(`INSERT INTO project(id,workspace_id,title) VALUES ($1,$2,'projection-run')`, project, h.workspace)
	runID := projectionFixtureUUID(h, 0x06, time.Now().UnixNano())
	runs := NewEnvDispatchRunStore(h.queries())
	run, err := runs.CreateRun(h.ctx, CreateEnvDispatchRunInput{
		RunID: runID, ProjectID: project, WorkspaceID: h.workspace,
		QuietWindowMS: 2_000, TotalTimeoutSeconds: 3_300,
	})
	require.NoError(h.t, err)
	_, err = runs.TransitionStatus(h.ctx, run.RunID, "provisioning", "preflight")
	require.NoError(h.t, err)
	_, err = runs.StartTimeout(h.ctx, run.RunID, time.Now().UTC())
	require.NoError(h.t, err)
	return run
}

func (h *universalDAGProjectionHarness) quietCandidate(run EnvDispatchRunRecord) {
	h.t.Helper()
	_, err := NewEnvDispatchRunStore(h.queries()).TransitionStatus(h.ctx, run.RunID, "running", "quiet_candidate")
	require.NoError(h.t, err)
}

// bindAgent creates the agent fixtures and one run agent binding.
func (h *universalDAGProjectionHarness) bindAgent(run EnvDispatchRunRecord, ordinal int) EnvDispatchRunAgentRecord {
	h.t.Helper()
	value := time.Now().UnixNano() + int64(ordinal)
	source := projectionFixtureUUID(h, 0x10, value)
	execution := projectionFixtureUUID(h, 0x20, value)
	runtime := projectionFixtureUUID(h, 0x30, value)
	h.exec(`INSERT INTO agent_runtime(id,workspace_id,name,runtime_mode,provider) VALUES ($1,$2,$3,'local','synthetic')`,
		runtime, h.workspace, fmt.Sprintf("runtime-%d-%d", ordinal, value))
	h.exec(`INSERT INTO agent(id,workspace_id,name,avatar_url,runtime_mode,runtime_id) VALUES ($1,$2,$3,'','local',$4)`,
		source, h.workspace, fmt.Sprintf("agent-%d-%d", ordinal, value), runtime)
	h.exec(`INSERT INTO agent(id,workspace_id,name,avatar_url,runtime_mode,runtime_id) VALUES ($1,$2,$3,'','local',$4)`,
		execution, h.workspace, fmt.Sprintf("execution-%d-%d", ordinal, value), runtime)
	record, err := NewEnvDispatchRunStore(h.queries()).BindRunAgent(h.ctx, BindEnvDispatchRunAgentInput{
		RunID: run.RunID, SourceAgentID: source, ExecutionAgentID: execution, RuntimeID: runtime,
		PiSessionID:     fmt.Sprintf("pi-session-%d", ordinal),
		TrainingMode:    "offline_rl",
		CaptureBoundary: fmt.Sprintf("boundary-%d", ordinal),
	})
	require.NoError(h.t, err)
	return record
}

func (h *universalDAGProjectionHarness) createTurn(run EnvDispatchRunRecord, agent EnvDispatchRunAgentRecord, ordinal int64) ResidentTurnRecord {
	h.t.Helper()
	turnID := projectionFixtureUUID(h, 0x40, time.Now().UnixNano()+ordinal)
	turn, err := NewEnvDispatchRunStore(h.queries()).CreateResidentTurn(h.ctx, CreateResidentTurnInput{
		TurnID: turnID, RunID: run.RunID, RunAgentID: agent.RunAgentID, Status: "active",
	})
	require.NoError(h.t, err)
	return turn
}

func (h *universalDAGProjectionHarness) insertCall(run EnvDispatchRunRecord, agent EnvDispatchRunAgentRecord, turn ResidentTurnRecord, callID string, ordinal int64) ProviderCallRecord {
	h.t.Helper()
	record, err := NewProviderCallLedger(h.queries(), h.pool).InsertProviderCall(h.ctx,
		mixedRLProviderCallInput(run.RunID, agent, turn, callID, ordinal))
	require.NoError(h.t, err)
	return record
}

// recordBoundary runs one canonical boundary in its own transaction.
func (h *universalDAGProjectionHarness) recordBoundary(input DAGBoundaryInput) DAGBoundaryResult {
	h.t.Helper()
	tx, err := h.pool.Begin(h.ctx)
	require.NoError(h.t, err)
	defer tx.Rollback(h.ctx)
	result, err := NewUniversalInteractionDAG().RecordBoundaryTx(h.ctx, db.New(tx), tx, input)
	require.NoError(h.t, err)
	require.NoError(h.t, tx.Commit(h.ctx))
	return result
}

func (h *universalDAGProjectionHarness) visibleBoundary(task db.AgentInboxEvent, run EnvDispatchRunRecord, agent EnvDispatchRunAgentRecord, actionID pgtype.UUID, endSeq int32, captureExpected bool) DAGBoundaryResult {
	h.t.Helper()
	input := DAGBoundaryInput{
		WorkspaceID: h.workspace, Task: task, BoundaryKind: DAGBoundaryVisible,
		CloseActionKind: DAGCloseMessage, EndSeq: endSeq,
		ActionID: actionID, ActionKey: "message:" + actionID.String(),
		MemoryTypeAtEvent: "graph", ProjectID: h.project,
		RunID: run.RunID, RunAgentID: agent.RunAgentID,
		ProviderCaptureExpected: captureExpected,
	}
	if captureExpected {
		input.ProviderCaptureCorrelationKey = agent.CaptureBoundary
	}
	return h.recordBoundary(input)
}

func (h *universalDAGProjectionHarness) inboundBoundary(task db.AgentInboxEvent, run EnvDispatchRunRecord, agent EnvDispatchRunAgentRecord, endSeq int32) DAGBoundaryResult {
	h.t.Helper()
	return h.recordBoundary(DAGBoundaryInput{
		WorkspaceID: h.workspace, Task: task, BoundaryKind: DAGBoundaryInbound,
		EndSeq: endSeq, MemoryTypeAtEvent: "graph", ProjectID: h.project,
		RunID: run.RunID, RunAgentID: agent.RunAgentID,
	})
}

func (h *universalDAGProjectionHarness) terminalBoundary(task db.AgentInboxEvent, run EnvDispatchRunRecord, agent EnvDispatchRunAgentRecord) DAGBoundaryResult {
	h.t.Helper()
	return h.recordBoundary(DAGBoundaryInput{
		WorkspaceID: h.workspace, Task: task, BoundaryKind: DAGBoundaryTerminal,
		CloseActionKind: DAGCloseTerminal, MemoryTypeAtEvent: "graph", ProjectID: h.project,
		RunID: run.RunID, RunAgentID: agent.RunAgentID,
	})
}

func (h *universalDAGProjectionHarness) attachCapture(segmentID, captureID string, links []ProviderCallAssociation) error {
	h.t.Helper()
	tx, err := h.pool.Begin(h.ctx)
	require.NoError(h.t, err)
	defer tx.Rollback(h.ctx)
	err = NewUniversalInteractionDAG().AttachProviderCaptureTx(h.ctx, db.New(tx), tx, segmentID, captureID, links)
	if err != nil && !errors.Is(err, ErrDAGProviderCaptureConflict) {
		return err
	}
	// A conflicted replay still commits: the durable conflict marker is part
	// of the capture contract the projector later refuses.
	commitErr := tx.Commit(h.ctx)
	if commitErr != nil {
		return commitErr
	}
	return err
}

func (h *universalDAGProjectionHarness) ownedLink(run EnvDispatchRunRecord, agent EnvDispatchRunAgentRecord, callID string, ordinal int64) ProviderCallAssociation {
	h.t.Helper()
	return ProviderCallAssociation{
		ProviderCallID: callID, Role: "owned", Ordinal: ordinal,
		RunID: run.RunID, RunAgentID: agent.RunAgentID,
		CaptureVersion: 1, CorrelationKey: agent.CaptureBoundary,
	}
}

func (h *universalDAGProjectionHarness) segmentByAction(action pgtype.UUID) db.InteractionDagSegment {
	h.t.Helper()
	segment, err := h.queries().GetUniversalDAGSegmentByVisibleAction(h.ctx, db.GetUniversalDAGSegmentByVisibleActionParams{
		WorkspaceID: h.workspace, VisibleActionKey: pgtype.Text{String: "message:" + action.String(), Valid: true},
	})
	require.NoError(h.t, err)
	return segment
}

// createChannelMessage materializes the real channel-message row a message
// consumption's foreign key requires, under the given canonical id, together
// with the human-owner chain the group-conversation invariants demand.
func (h *universalDAGProjectionHarness) createChannelMessage(id pgtype.UUID) {
	h.t.Helper()
	owner := projectionFixtureUUID(h, 0x60, time.Now().UnixNano())
	h.exec(`INSERT INTO "user"(id,name,email) VALUES ($1,'projection',$2)`, owner,
		fmt.Sprintf("projection-%d@example.invalid", time.Now().UnixNano()))
	h.exec(`INSERT INTO member(workspace_id,user_id,role) VALUES ($1,$2,'owner')`, h.workspace, owner)
	channel := projectionFixtureUUID(h, 0x61, time.Now().UnixNano())
	h.exec(`INSERT INTO channel(id,workspace_id,name,created_by) VALUES ($1,$2,'projection',$3)`, channel, h.workspace, owner)
	// Channel triggers may auto-enroll the creator as the owning member.
	h.exec(`INSERT INTO channel_member(channel_id,workspace_id,member_type,member_id,role)
VALUES ($1,$2,'user',$3,'owner') ON CONFLICT DO NOTHING`, channel, h.workspace, owner)
	// Conversations are 1:1 with their channel and may be auto-created by
	// channel triggers; reuse the existing row when present.
	var conversation pgtype.UUID
	err := h.pool.QueryRow(h.ctx, `SELECT id FROM conversation WHERE channel_id=$1`, channel).Scan(&conversation)
	if err != nil {
		require.ErrorIs(h.t, err, pgx.ErrNoRows)
		conversation = projectionFixtureUUID(h, 0x62, time.Now().UnixNano())
		h.exec(`INSERT INTO conversation(id,workspace_id,kind,channel_id) VALUES ($1,$2,'group',$3)`, conversation, h.workspace, channel)
	}
	h.exec(`INSERT INTO conversation_member(conversation_id,workspace_id,member_type,member_id)
VALUES ($1,$2,'user',$3) ON CONFLICT DO NOTHING`, conversation, h.workspace, owner)
	h.exec(`INSERT INTO channel_message(id,channel_id,workspace_id,author_type,content,conversation_id,seq)
VALUES ($1,$2,$3,'agent','projection',$4,1)`, id, channel, h.workspace, conversation)
}

func projectionUniversalMappings(t *testing.T, h *universalDAGProjectionHarness, runID pgtype.UUID) map[string]bool {
	t.Helper()
	rows, err := h.pool.Query(h.ctx, `
SELECT universal_segment_id FROM interaction_dag_run_segment
WHERE run_id = $1 AND universal_segment_id IS NOT NULL`, runID)
	require.NoError(t, err)
	defer rows.Close()
	out := make(map[string]bool)
	for rows.Next() {
		var id string
		require.NoError(t, rows.Scan(&id))
		out[id] = true
	}
	require.NoError(t, rows.Err())
	return out
}

// TestMixedRLFreeze_ProjectsCanonicalRunIntoSnapshot drives one run holding
// every projection shape — two visible message segments with captured calls,
// one task-closed terminal segment, one action-less agent whose terminal is
// only a freeze artifact, and one message consumption edge — then requires
// the frozen snapshot to be the deterministic projection of the canonical
// store: mapped segment identities, mirrored ownership, merged terminal
// bucket, and edges resolved through canonical actions.
func TestMixedRLFreeze_ProjectsCanonicalRunIntoSnapshot(t *testing.T) {
	h := newUniversalDAGProjectionHarness(t)
	run := h.createRun()
	first := h.bindAgent(run, 1)
	second := h.bindAgent(run, 2)
	third := h.bindAgent(run, 3)

	firstTask := h.createTask("channel_message")
	h.addTaskMessage(firstTask, 1)
	h.addTaskMessage(firstTask, 2)
	firstAction := util.MustParseUUID("81000000-0000-4000-8000-000000000001")
	secondAction := util.MustParseUUID("81000000-0000-4000-8000-000000000002")
	firstMessage := h.visibleBoundary(firstTask, run, first, firstAction, 1, true)
	secondMessage := h.visibleBoundary(firstTask, run, first, secondAction, 2, true)
	h.terminalBoundary(firstTask, run, first)

	secondTask := h.createTask("channel_message")
	h.addTaskMessage(secondTask, 1)
	h.inboundBoundary(secondTask, run, second, 1)
	secondTerminal := h.terminalBoundary(secondTask, run, second)

	thirdTask := h.createTask("channel_message")
	h.addTaskMessage(thirdTask, 1)
	thirdAction := util.MustParseUUID("81000000-0000-4000-8000-000000000003")
	thirdMessage := h.visibleBoundary(thirdTask, run, third, thirdAction, 1, false)
	h.terminalBoundary(thirdTask, run, third)

	firstTurn := h.createTurn(run, first, 1)
	h.insertCall(run, first, firstTurn, "proj-call-1", 1)
	h.insertCall(run, first, firstTurn, "proj-call-2", 2)
	secondTurn := h.createTurn(run, second, 2)
	h.insertCall(run, second, secondTurn, "proj-call-3", 1)
	thirdTurn := h.createTurn(run, third, 3)
	h.insertCall(run, third, thirdTurn, "proj-call-4", 1)

	require.NoError(t, h.attachCapture(firstMessage.SegmentID, "capture-first",
		[]ProviderCallAssociation{h.ownedLink(run, first, "proj-call-1", 1)}))
	require.NoError(t, h.attachCapture(secondMessage.SegmentID, "capture-second",
		[]ProviderCallAssociation{h.ownedLink(run, first, "proj-call-2", 2)}))

	h.createChannelMessage(firstAction)
	_, err := NewProviderCallLedger(h.queries(), h.pool).InsertMessageConsumption(h.ctx, MessageConsumptionInput{
		ConsumptionID: projectionFixtureUUID(h, 0x50, time.Now().UnixNano()),
		RunID:         run.RunID, RunAgentID: second.RunAgentID, TurnID: secondTurn.TurnID,
		ChannelMessageID: firstAction, Source: "message_check",
		// The fixture calls start at 2026-08-10 02:00 UTC; a consumption must
		// predate the call that consumed the message.
		EffectiveFromCallID: "proj-call-3",
		ConsumedAt:          time.Date(2026, time.August, 10, 1, 59, 59, 0, time.UTC),
	})
	require.NoError(t, err)

	h.quietCandidate(run)
	result, err := NewMixedRLFreezeService(h.queries(), h.pool).Freeze(h.ctx, run.RunID, false)
	require.NoError(t, err)
	dag, err := NewProviderCallLedger(h.queries(), h.pool).GetFrozenDAG(h.ctx, run.RunID, result.Snapshot.SnapshotID)
	require.NoError(t, err)

	segmentsByID := make(map[string]FrozenDAGSegmentRecord, len(dag.Segments))
	for _, segment := range dag.Segments {
		segmentsByID[segment.SegmentID] = segment
	}
	require.Len(t, dag.Segments, 5)

	// Message segments project under their canonical Universal identity with
	// the canonical action id carried through.
	assert.Equal(t, "message", segmentsByID[firstMessage.SegmentID].Kind)
	assert.Equal(t, firstAction, segmentsByID[firstMessage.SegmentID].CanonicalActionID)
	assert.Equal(t, "message", segmentsByID[secondMessage.SegmentID].Kind)
	assert.Equal(t, "message", segmentsByID[thirdMessage.SegmentID].Kind)
	assert.Equal(t, thirdAction, segmentsByID[thirdMessage.SegmentID].CanonicalActionID)

	// The task-closed terminal segment projects and becomes the terminal
	// bucket for its run agent's unowned call, instead of a synthetic row.
	assert.Equal(t, "terminal", segmentsByID[secondTerminal.SegmentID].Kind)
	assert.False(t, segmentsByID[secondTerminal.SegmentID].CanonicalActionID.Valid)

	// The third agent's action-less turn leaves its call unowned, so freeze
	// keeps the derived synthetic terminal bucket.
	syntheticID := "terminal:" + third.RunAgentID.String()
	require.Contains(t, segmentsByID, syntheticID)
	assert.Equal(t, "terminal", segmentsByID[syntheticID].Kind)

	// Every canonical segment in run scope is mapped exactly once.
	assert.Equal(t, map[string]bool{
		firstMessage.SegmentID:   true,
		secondMessage.SegmentID:  true,
		thirdMessage.SegmentID:   true,
		secondTerminal.SegmentID: true,
	}, projectionUniversalMappings(t, h, run.RunID))

	// Ownership mirrors the canonical association set, including both
	// terminal buckets.
	assocByCall := make(map[string]string, len(dag.Associations))
	for _, association := range dag.Associations {
		assocByCall[association.ProviderCallID] = association.AssociationKind
	}
	assert.Equal(t, map[string]string{
		"proj-call-1": "owned", "proj-call-2": "owned",
		"proj-call-3": "owned", "proj-call-4": "owned",
	}, assocByCall)

	// Edges resolve endpoints through canonical identities: the consumed
	// first-agent message feeds the second agent's segment, and consecutive
	// owned calls inside one session chain a continuation edge.
	channelMessageEdges, continuationEdges := 0, 0
	for _, edge := range dag.Edges {
		switch edge.Type {
		case "channel_message":
			channelMessageEdges++
			assert.Equal(t, firstAction, edge.TriggerMessageID)
			assert.Equal(t, firstMessage.SegmentID, edge.SourceSegmentID)
			assert.Equal(t, secondTerminal.SegmentID, edge.DestinationSegmentID)
		case "session_continuation":
			continuationEdges++
			assert.Equal(t, firstMessage.SegmentID, edge.SourceSegmentID)
			assert.Equal(t, secondMessage.SegmentID, edge.DestinationSegmentID)
		}
	}
	assert.Equal(t, 1, channelMessageEdges, "channel_message edge through canonical action")
	assert.Equal(t, 1, continuationEdges, "session continuation edge")
}

// TestMixedRLFreeze_FailsClosedOnProjectionMismatchOnFaithfulSchema requires
// freeze to refuse a frozen-projection row that the canonical store cannot
// confirm, instead of silently keeping a second source of truth.
func TestMixedRLFreeze_FailsClosedOnProjectionMismatchOnFaithfulSchema(t *testing.T) {
	h := newUniversalDAGProjectionHarness(t)
	run := h.createRun()
	agent := h.bindAgent(run, 1)
	task := h.createTask("channel_message")
	h.addTaskMessage(task, 1)
	action := util.MustParseUUID("82000000-0000-4000-8000-000000000001")
	boundary := h.visibleBoundary(task, run, agent, action, 1, true)
	turn := h.createTurn(run, agent, 1)
	call := h.insertCall(run, agent, turn, "mismatch-call", 1)
	require.NoError(t, h.attachCapture(boundary.SegmentID, "capture-mismatch",
		[]ProviderCallAssociation{h.ownedLink(run, agent, call.CallID, 1)}))

	// A stale frozen row claims a canonical action the canonical store never
	// recorded for this run — dual-write residue freeze must reject.
	staleAction := util.MustParseUUID("82000000-0000-4000-8000-000000000002")
	h.exec(`INSERT INTO interaction_dag_run_segment(segment_id,run_id,run_agent_id,kind,canonical_action_id,segment_ordinal)
VALUES ('message:stale', $1, $2, 'message', $3, 1)`, run.RunID, agent.RunAgentID, staleAction)

	h.quietCandidate(run)
	_, err := NewMixedRLFreezeService(h.queries(), h.pool).Freeze(h.ctx, run.RunID, false)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "projection")

	var snapshots int
	require.NoError(t, h.pool.QueryRow(h.ctx,
		`SELECT count(*) FROM interaction_dag_frozen_snapshot WHERE run_id = $1`, run.RunID).Scan(&snapshots))
	assert.Zero(t, snapshots, "mismatched freeze must not publish a snapshot")
}

// TestMixedRLFreeze_HealsLegacyFrozenRowsByCanonicalAction requires the
// projection to adopt a pre-465 frozen row that describes the same canonical
// action instead of duplicating it.
func TestMixedRLFreeze_HealsLegacyFrozenRowsByCanonicalAction(t *testing.T) {
	h := newUniversalDAGProjectionHarness(t)
	run := h.createRun()
	agent := h.bindAgent(run, 1)
	task := h.createTask("channel_message")
	h.addTaskMessage(task, 1)
	action := util.MustParseUUID("83000000-0000-4000-8000-000000000001")
	boundary := h.visibleBoundary(task, run, agent, action, 1, true)
	turn := h.createTurn(run, agent, 1)
	call := h.insertCall(run, agent, turn, "heal-call", 1)
	require.NoError(t, h.attachCapture(boundary.SegmentID, "capture-heal",
		[]ProviderCallAssociation{h.ownedLink(run, agent, call.CallID, 1)}))

	legacyID := "message:" + action.String()
	h.exec(`INSERT INTO interaction_dag_run_segment(segment_id,run_id,run_agent_id,kind,canonical_action_id,segment_ordinal)
VALUES ($1, $2, $3, 'message', $4, 1)`, legacyID, run.RunID, agent.RunAgentID, action)

	h.quietCandidate(run)
	frozen, err := NewMixedRLFreezeService(h.queries(), h.pool).Freeze(h.ctx, run.RunID, false)
	require.NoError(t, err)
	dag, err := NewProviderCallLedger(h.queries(), h.pool).GetFrozenDAG(h.ctx, run.RunID, frozen.Snapshot.SnapshotID)
	require.NoError(t, err)

	var mapped string
	require.NoError(t, h.pool.QueryRow(h.ctx, `
SELECT universal_segment_id FROM interaction_dag_run_segment
WHERE run_id = $1 AND segment_id = $2`, run.RunID, legacyID).Scan(&mapped))
	assert.Equal(t, boundary.SegmentID, mapped, "legacy row must be healed onto the canonical mapping")
	require.Len(t, dag.Segments, 1, "legacy message row only; its call is owned so no terminal bucket")
	assert.Equal(t, legacyID, dag.Segments[0].SegmentID, "healed legacy row remains the frozen identity")
}

// TestUniversalDAGProjectionMigration_AddsMappingColumns requires migration
// 465 to add the canonical mapping columns, their uniqueness, and the
// explicit backfill audit surface.
func TestUniversalDAGProjectionMigration_AddsMappingColumns(t *testing.T) {
	h := newUniversalDAGProjectionHarness(t)
	for table, column := range map[string]string{
		"interaction_dag_run_segment": "universal_segment_id",
		"interaction_dag_causal_edge": "universal_edge_id",
	} {
		var exists bool
		require.NoError(h.t, h.pool.QueryRow(h.ctx, `
SELECT EXISTS (SELECT 1 FROM information_schema.columns
WHERE table_schema = current_schema() AND table_name = $1 AND column_name = $2)`,
			table, column).Scan(&exists))
		assert.True(t, exists, "%s.%s", table, column)
	}
	var auditExists bool
	require.NoError(h.t, h.pool.QueryRow(h.ctx, `
SELECT EXISTS (SELECT 1 FROM information_schema.tables
WHERE table_schema = current_schema() AND table_name = 'interaction_dag_projection_backfill_audit')`).Scan(&auditExists))
	assert.True(t, auditExists, "backfill audit table")

	// The mapping is unique per run: two frozen rows cannot claim one
	// canonical segment.
	run := h.createRun()
	agent := h.bindAgent(run, 1)
	task := h.createTask("channel_message")
	h.addTaskMessage(task, 1)
	action := util.MustParseUUID("84000000-0000-4000-8000-000000000001")
	boundary := h.visibleBoundary(task, run, agent, action, 1, false)
	h.exec(`INSERT INTO interaction_dag_run_segment(segment_id,run_id,run_agent_id,kind,segment_ordinal,universal_segment_id)
VALUES ('dup-a', $1, $2, 'terminal', 1, $3)`, run.RunID, agent.RunAgentID, boundary.SegmentID)
	_, err := h.pool.Exec(h.ctx, `
INSERT INTO interaction_dag_run_segment(segment_id,run_id,run_agent_id,kind,segment_ordinal,universal_segment_id)
VALUES ('dup-b', $1, $2, 'terminal', 2, $3)`, run.RunID, agent.RunAgentID, boundary.SegmentID)
	require.Error(t, err, "duplicate universal mapping must be rejected")
}

// projectSnapshot runs one projector pass inside a fresh transaction.
func (h *universalDAGProjectionHarness) projectSnapshot(run EnvDispatchRunRecord) (ProjectedRunSnapshot, error) {
	h.t.Helper()
	tx, err := h.pool.Begin(h.ctx)
	if err != nil {
		return ProjectedRunSnapshot{}, err
	}
	defer tx.Rollback(h.ctx)
	projected, err := NewUniversalDAGFrozenProjector().ProjectRunSnapshot(h.ctx, db.New(tx), h.workspace, run.RunID)
	if err != nil {
		return ProjectedRunSnapshot{}, err
	}
	if err := tx.Commit(h.ctx); err != nil {
		return ProjectedRunSnapshot{}, err
	}
	return projected, nil
}

// TestFrozenProjector_ProjectRunSnapshotRejectsUnsettledCapture requires the
// projector gate to refuse pending, conflicted, and owned-less finalized
// captures before any projection row is written.
func TestFrozenProjector_ProjectRunSnapshotRejectsUnsettledCapture(t *testing.T) {
	h := newUniversalDAGProjectionHarness(t)

	pendingRun := h.createRun()
	pendingAgent := h.bindAgent(pendingRun, 1)
	pendingTask := h.createTask("channel_message")
	h.addTaskMessage(pendingTask, 1)
	pendingAction := util.MustParseUUID("85000000-0000-4000-8000-000000000001")
	h.visibleBoundary(pendingTask, pendingRun, pendingAgent, pendingAction, 1, true)
	_, err := h.projectSnapshot(pendingRun)
	require.ErrorIs(t, err, ErrDAGProjectionCapturePending)

	conflictRun := h.createRun()
	conflictAgent := h.bindAgent(conflictRun, 1)
	conflictTask := h.createTask("channel_message")
	h.addTaskMessage(conflictTask, 1)
	conflictAction := util.MustParseUUID("85000000-0000-4000-8000-000000000002")
	conflictBoundary := h.visibleBoundary(conflictTask, conflictRun, conflictAgent, conflictAction, 1, true)
	conflictTurn := h.createTurn(conflictRun, conflictAgent, 1)
	conflictCall := h.insertCall(conflictRun, conflictAgent, conflictTurn, "conflict-call", 1)
	require.NoError(t, h.attachCapture(conflictBoundary.SegmentID, "capture-one",
		[]ProviderCallAssociation{h.ownedLink(conflictRun, conflictAgent, conflictCall.CallID, 1)}))
	// A second, mismatched replay marks the capture conflicted.
	require.Error(t, h.attachCapture(conflictBoundary.SegmentID, "capture-two",
		[]ProviderCallAssociation{h.ownedLink(conflictRun, conflictAgent, conflictCall.CallID, 1)}))
	_, err = h.projectSnapshot(conflictRun)
	require.ErrorIs(t, err, ErrDAGProjectionCaptureConflict)

	missingRun := h.createRun()
	missingAgent := h.bindAgent(missingRun, 1)
	missingTask := h.createTask("channel_message")
	h.addTaskMessage(missingTask, 1)
	missingAction := util.MustParseUUID("85000000-0000-4000-8000-000000000003")
	missingBoundary := h.visibleBoundary(missingTask, missingRun, missingAgent, missingAction, 1, true)
	missingTurn := h.createTurn(missingRun, missingAgent, 1)
	missingCall := h.insertCall(missingRun, missingAgent, missingTurn, "missing-call", 1)
	require.NoError(t, h.attachCapture(missingBoundary.SegmentID, "capture-audit-only", []ProviderCallAssociation{{
		ProviderCallID: missingCall.CallID, Role: "audit", Ordinal: 1,
		RunID: missingRun.RunID, RunAgentID: missingAgent.RunAgentID,
		CaptureVersion: 1, CorrelationKey: missingAgent.CaptureBoundary,
	}}))
	_, err = h.projectSnapshot(missingRun)
	require.ErrorIs(t, err, ErrDAGProjectionCaptureMissing)
}

// TestFrozenProjector_ProjectRunSnapshotIsIdempotent requires repeated
// projector passes over one settled run to keep a single frozen row per
// canonical segment.
func TestFrozenProjector_ProjectRunSnapshotIsIdempotent(t *testing.T) {
	h := newUniversalDAGProjectionHarness(t)
	run := h.createRun()
	agent := h.bindAgent(run, 1)
	task := h.createTask("channel_message")
	h.addTaskMessage(task, 1)
	action := util.MustParseUUID("86000000-0000-4000-8000-000000000001")
	boundary := h.visibleBoundary(task, run, agent, action, 1, true)
	turn := h.createTurn(run, agent, 1)
	call := h.insertCall(run, agent, turn, "idempotent-call", 1)
	require.NoError(t, h.attachCapture(boundary.SegmentID, "capture-idempotent",
		[]ProviderCallAssociation{h.ownedLink(run, agent, call.CallID, 1)}))

	first, err := h.projectSnapshot(run)
	require.NoError(t, err)
	require.Len(t, first.Segments, 1)
	assert.Equal(t, boundary.SegmentID, first.Segments[0].SegmentID)
	assert.Equal(t, boundary.SegmentID, first.ByUniversalID[boundary.SegmentID])
	assert.Equal(t, boundary.SegmentID, first.ByCanonicalID[action])

	second, err := h.projectSnapshot(run)
	require.NoError(t, err)
	assert.Len(t, second.Segments, 1, "second pass must not duplicate rows")
	var rows int
	require.NoError(t, h.pool.QueryRow(h.ctx,
		`SELECT count(*) FROM interaction_dag_run_segment WHERE run_id = $1`, run.RunID).Scan(&rows))
	assert.Equal(t, 1, rows)
}
