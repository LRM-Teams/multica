// SPDX-License-Identifier: Apache-2.0

package researchrun

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
)

// Phase 2 slice P2.2 (unification spec §5): the Director Brief carries a
// background-knowledge block recalled from the memory graphs, with epistemic
// status and observation date per entry, guidance that it is planning
// reference rather than evidence, and provider failures degrading to an empty
// block that never blocks the cycle.

type backgroundKnowledgeStub struct {
	calls           int
	goal, projectID string
	entries         []V6BackgroundKnowledgeEntry
	err             error
}

func (s *backgroundKnowledgeStub) BackgroundKnowledge(ctx context.Context, workspaceID, runID, goal, projectID string) ([]V6BackgroundKnowledgeEntry, error) {
	s.calls++
	s.goal, s.projectID = goal, projectID
	return s.entries, s.err
}

func backgroundFactsWithKnowledge(entries []any) DirectorBriefFacts {
	return DirectorBriefFacts{
		WorkspaceID: "00000000-0000-4000-8000-000000000001", RunID: "00000000-0000-4000-8000-000000000002",
		AssignmentID: "00000000-0000-4000-8000-000000000003", DirectorGeneration: 1, StateVersion: 1,
		Goal:          map[string]any{"goal_version": 1, "goal": "Research", "scope": map[string]any{}, "audience": "", "freshness": "", "language": "en", "source_policy": map[string]any{}},
		DirectorState: "available", Team: []any{map[string]any{"agent_id": "00000000-0000-4000-8000-000000000004", "membership_id": "00000000-0000-4000-8000-000000000005", "state": "idle", "mission_summary": "Direct"}},
		Branches: []any{}, TerminalSummaries: []any{}, WorkItems: []any{}, Discussions: []any{}, Reports: []any{},
		UnresolvedDisputes: []any{}, Steering: []any{},
		BackgroundKnowledge: entries,
	}
}

// The compiler renders the background-knowledge block on every page: entries
// keep their node reference, graph tag, epistemic status, observation date and
// bounded summary, and the guidance states the block is planning reference,
// never evidence. An empty fact list still renders the stable empty block.
func TestCompileDirectorBriefRendersBackgroundKnowledge(t *testing.T) {
	entries := []any{
		map[string]any{"node_id": "res-1", "graph": "research", "epistemic": "accepted", "observed_at_date": "2026-08-15", "summary": "pools exhaust under load"},
		map[string]any{"node_id": "res-2", "graph": "research", "epistemic": "proposed", "observed_at_date": "2026-08-20", "summary": "retries correlate"},
		map[string]any{"node_id": "prj-1", "graph": "project", "epistemic": "supported", "observed_at_date": "2026-08-22", "summary": "queue depth grows"},
	}
	compiled, err := (contextCompilerModule{}).CompileDirectorBrief(backgroundFactsWithKnowledge(entries), time.Unix(1, 0))
	if err != nil {
		t.Fatalf("compile: %v", err)
	}
	if len(compiled.Pages) != 1 {
		t.Fatalf("pages=%d, want 1", len(compiled.Pages))
	}
	var page map[string]any
	if err := json.Unmarshal(compiled.Pages[0], &page); err != nil {
		t.Fatal(err)
	}
	background, ok := page["background_knowledge"].(map[string]any)
	if !ok {
		t.Fatalf("page lacks background_knowledge block: %v", page)
	}
	guidance, _ := background["guidance"].(string)
	if !strings.Contains(guidance, "仅供规划参考") || !strings.Contains(guidance, "不作为证据") {
		t.Fatalf("guidance=%q, want planning-reference and not-evidence wording", guidance)
	}
	rendered, _ := background["entries"].([]any)
	if len(rendered) != 3 {
		t.Fatalf("entries=%d, want 3", len(rendered))
	}
	first, _ := rendered[0].(map[string]any)
	for _, key := range []string{"node_id", "graph", "epistemic", "observed_at_date", "summary"} {
		if _, present := first[key]; !present {
			t.Fatalf("entry lacks %q: %v", key, first)
		}
	}
	if first["node_id"] != "res-1" || first["observed_at_date"] != "2026-08-15" {
		t.Fatalf("entry=%v, want res-1 observed 2026-08-15", first)
	}

	empty, err := (contextCompilerModule{}).CompileDirectorBrief(backgroundFactsWithKnowledge(nil), time.Unix(1, 0))
	if err != nil {
		t.Fatalf("compile without background: %v", err)
	}
	var emptyPage map[string]any
	if err := json.Unmarshal(empty.Pages[0], &emptyPage); err != nil {
		t.Fatal(err)
	}
	block, _ := emptyPage["background_knowledge"].(map[string]any)
	if block == nil {
		t.Fatalf("empty facts must still render the background block: %v", emptyPage)
	}
	if entries, _ := block["entries"].([]any); len(entries) != 0 {
		t.Fatalf("entries=%d, want the stable empty list", len(entries))
	}
}

// prepareBackgroundSession installs the V6 Director chain on the fixture run
// and binds the session to a project (empty projectID leaves it unbound),
// returning the fresh state version.
func prepareBackgroundSession(t *testing.T, run *transactionRecoveryRun, projectID string) int64 {
	t.Helper()
	binding := any(nil)
	if projectID != "" {
		binding = projectID
	}
	if _, err := run.pool.Exec(run.ctx, `UPDATE research_session SET orchestrator_version='research-run-v6', project_id=$2 WHERE id=$1::uuid`, run.fixture.sessionID, binding); err != nil {
		t.Fatal(err)
	}
	if _, err := run.store.AssignV6Director(run.ctx, AssignV6DirectorInput{
		WorkspaceID: run.fixture.workspaceID, RunID: run.fixture.sessionID,
		AgentID: run.fixture.agentID, UserID: run.fixture.userID,
		Reason: "Compile background knowledge", ClientRequestID: uuid.NewString(), ExpectedStateVersion: 0,
	}); err != nil {
		t.Fatal(err)
	}
	var stateVersion int64
	if err := run.pool.QueryRow(run.ctx, `SELECT state_version FROM research_session WHERE id=$1::uuid`, run.fixture.sessionID).Scan(&stateVersion); err != nil {
		t.Fatal(err)
	}
	return stateVersion
}

// Facts loading recalls through the injected provider: the query is the run
// goal, the project binding comes from research_session.project_id, and the
// entries land in the facts that the compiler renders into persisted pages.
func TestDirectorBriefLoadsBackgroundKnowledge(t *testing.T) {
	run := newTransactionRecoveryRun(t, "V6 Director Brief background knowledge")
	projectID := uuid.NewString()
	if _, err := run.pool.Exec(run.ctx, `INSERT INTO project (id, workspace_id, title) VALUES ($1::uuid, $2::uuid, 'Background project')`, projectID, run.fixture.workspaceID); err != nil {
		t.Fatal(err)
	}
	stateVersion := prepareBackgroundSession(t, run, projectID)

	observed := time.Date(2026, 8, 15, 9, 30, 0, 0, time.UTC)
	stub := &backgroundKnowledgeStub{entries: []V6BackgroundKnowledgeEntry{
		{NodeID: "res-1", Graph: "research", Epistemic: "accepted", ObservedAt: observed, Summary: "pools exhaust under load"},
		{NodeID: "prj-1", Graph: "project", Epistemic: "supported", ObservedAt: observed, Summary: "queue depth grows"},
	}}
	run.store.SetBackgroundKnowledgeProvider(stub)

	input := StartV6DirectorCycleInput{
		WorkspaceID: run.fixture.workspaceID, RunID: run.fixture.sessionID, ExpectedStateVersion: stateVersion,
	}
	facts, err := run.store.LoadDirectorBriefFacts(run.ctx, input)
	if err != nil {
		t.Fatalf("LoadDirectorBriefFacts: %v", err)
	}
	if stub.calls != 1 {
		t.Fatalf("provider calls=%d, want 1", stub.calls)
	}
	if stub.goal != "Compare the evidence" {
		t.Fatalf("provider goal=%q, want the run goal", stub.goal)
	}
	if stub.projectID != projectID {
		t.Fatalf("provider projectID=%q, want the session binding %s", stub.projectID, projectID)
	}
	if len(facts.BackgroundKnowledge) != 2 {
		t.Fatalf("facts background entries=%d, want 2", len(facts.BackgroundKnowledge))
	}
	first, _ := facts.BackgroundKnowledge[0].(map[string]any)
	if first["node_id"] != "res-1" || first["observed_at_date"] != "2026-08-15" {
		t.Fatalf("facts entry=%v, want res-1 with the observation date", first)
	}

	input.TriggerKey = "background-knowledge"
	input.FromSequence, input.ThroughSequence, input.ExpectedStateVersion = backgroundCycleFencing(run)
	cycle, err := (directorBriefModule{store: run.store, compiler: contextCompilerModule{}}).Start(run.ctx, input)
	if err != nil {
		t.Fatalf("Start director cycle: %v", err)
	}
	// content_bytes is bytea: scan the raw bytes, not the hex-escaped text.
	var content []byte
	if err := run.pool.QueryRow(run.ctx, `
		SELECT content_bytes FROM research_director_brief_page
		WHERE workspace_id=$1::uuid AND session_id=$2::uuid AND director_cycle_id=$3::uuid AND ordinal=0`,
		run.fixture.workspaceID, run.fixture.sessionID, cycle.ID).Scan(&content); err != nil {
		t.Fatalf("persisted page: %v", err)
	}
	page := string(content)
	for _, want := range []string{"background_knowledge", "res-1", "prj-1", "2026-08-15"} {
		if !strings.Contains(page, want) {
			t.Fatalf("persisted page lacks %q", want)
		}
	}
}

// An unbound session passes an empty project id: the provider decides scope,
// and the loader stays a pure conduit for the binding.
func TestDirectorBriefUnboundSessionPassesEmptyProject(t *testing.T) {
	run := newTransactionRecoveryRun(t, "V6 Director Brief unbound background")
	stateVersion := prepareBackgroundSession(t, run, "")

	stub := &backgroundKnowledgeStub{}
	run.store.SetBackgroundKnowledgeProvider(stub)
	_, err := run.store.LoadDirectorBriefFacts(run.ctx, StartV6DirectorCycleInput{
		WorkspaceID: run.fixture.workspaceID, RunID: run.fixture.sessionID, ExpectedStateVersion: stateVersion,
	})
	if err != nil {
		t.Fatalf("LoadDirectorBriefFacts: %v", err)
	}
	if stub.projectID != "" {
		t.Fatalf("provider projectID=%q, want empty for an unbound session", stub.projectID)
	}
}

// A provider failure degrades to an empty block: facts loading still succeeds
// and the Director cycle completes, so background recall never blocks planning.
func TestDirectorBriefBackgroundKnowledgeProviderFailureDegrades(t *testing.T) {
	run := newTransactionRecoveryRun(t, "V6 Director Brief background failure")
	stateVersion := prepareBackgroundSession(t, run, "")
	run.store.SetBackgroundKnowledgeProvider(&backgroundKnowledgeStub{err: errors.New("graph unavailable")})

	input := StartV6DirectorCycleInput{
		WorkspaceID: run.fixture.workspaceID, RunID: run.fixture.sessionID, ExpectedStateVersion: stateVersion,
	}
	facts, err := run.store.LoadDirectorBriefFacts(run.ctx, input)
	if err != nil {
		t.Fatalf("facts loading must survive a provider failure: %v", err)
	}
	if len(facts.BackgroundKnowledge) != 0 {
		t.Fatalf("entries=%d, want the degraded empty list", len(facts.BackgroundKnowledge))
	}
	input.TriggerKey = "background-failure"
	input.FromSequence, input.ThroughSequence, input.ExpectedStateVersion = backgroundCycleFencing(run)
	if _, err := (directorBriefModule{store: run.store, compiler: contextCompilerModule{}}).Start(run.ctx, input); err != nil {
		t.Fatalf("cycle must complete without background: %v", err)
	}
}

// backgroundCycleFencing returns the (from, through, stateVersion) triple the
// cycle persistence check requires for the session's current state.
func backgroundCycleFencing(run *transactionRecoveryRun) (int64, int64, int64) {
	var through, version int64
	if err := run.pool.QueryRow(run.ctx, `
		SELECT state_version, COALESCE((SELECT max(sequence) FROM research_run_event WHERE session_id=s.id),0)
		FROM research_session s WHERE s.id=$1::uuid`,
		run.fixture.sessionID).Scan(&version, &through); err != nil {
		return 0, 0, 0
	}
	return through, through, version
}
