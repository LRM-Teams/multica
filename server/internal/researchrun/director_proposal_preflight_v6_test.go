package researchrun

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/google/uuid"
)

func TestValidateV6ParallelResearchPlanRequiresStaffingAndParallelWork(t *testing.T) {
	tests := []struct {
		name    string
		facts   v6DirectorPreflightFacts
		wantErr string
	}{
		{
			name:    "one initial Agent is not a research team",
			facts:   v6DirectorPreflightFacts{maxParallelTasks: 5, proposedAgentCount: 1},
			wantErr: "至少需要 3 个不同的 run-scoped Agent",
		},
		{
			name:  "three initial Agents satisfy staffing",
			facts: v6DirectorPreflightFacts{maxParallelTasks: 5, proposedAgentCount: 3},
		},
		{
			name:    "one broad Work is not parallel research",
			facts:   v6DirectorPreflightFacts{maxParallelTasks: 5, workerCount: 3, proposedAtomicWork: 1},
			wantErr: "至少需要 3 个分配给不同 Agent 的 atomic Work",
		},
		{
			name:  "three independent Work Items satisfy first round",
			facts: v6DirectorPreflightFacts{maxParallelTasks: 5, workerCount: 3, proposedAtomicWork: 3},
		},
		{
			name:    "open questions require distinct follow-up Work",
			facts:   v6DirectorPreflightFacts{maxParallelTasks: 5, workerCount: 3, resultCount: 1, unresolvedQuestions: 3, proposedAtomicWork: 2},
			wantErr: "至少需要 3 个分配给不同 Agent 的 atomic Work",
		},
		{
			name:  "pending Agent creation permits a staffing cycle",
			facts: v6DirectorPreflightFacts{maxParallelTasks: 5, workerCount: 1, pendingAgentCount: 2, proposedAtomicWork: 1},
		},
		{
			name: "convergence may consume unresolved frontier nodes before more research",
			facts: v6DirectorPreflightFacts{maxParallelTasks: 5, workerCount: 3, resultCount: 2,
				unresolvedQuestions: 4, proposedConvergence: 1},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validateV6ParallelResearchPlan(tt.facts)
			if tt.wantErr == "" && err != nil {
				t.Fatalf("validate plan: %v", err)
			}
			if tt.wantErr != "" && (!errors.Is(err, ErrInvalidContract) || !strings.Contains(err.Error(), tt.wantErr)) {
				t.Fatalf("error=%v want ErrInvalidContract containing %q", err, tt.wantErr)
			}
		})
	}
}

func TestPreflightV6DirectorProposalRejectsMissingBranchBeforeMutation(t *testing.T) {
	run := newTransactionRecoveryRun(t, "Preflight Director Branch references")
	if _, err := run.pool.Exec(run.ctx, `UPDATE research_session SET orchestrator_version='research-run-v6' WHERE id=$1::uuid`, run.fixture.sessionID); err != nil {
		t.Fatal(err)
	}
	if _, err := run.store.AssignV6Director(run.ctx, AssignV6DirectorInput{
		WorkspaceID: run.fixture.workspaceID, RunID: run.fixture.sessionID,
		AgentID: run.fixture.agentID, UserID: run.fixture.userID,
		Reason: "Validate all references", ClientRequestID: uuid.NewString(), ExpectedStateVersion: 0,
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := run.store.AddV6TeamMember(run.ctx, AddV6TeamMemberInput{
		WorkspaceID: run.fixture.workspaceID, RunID: run.fixture.sessionID,
		AgentID: run.fixture.reporterID, MissionPrompt: "Investigate one direction",
	}); err != nil {
		t.Fatal(err)
	}
	payload, err := json.Marshal(v6CreateWorkActionPayload{
		Kind: "research", AssigneeAgentID: run.fixture.reporterID, Mission: "Investigate one direction",
		ExpectedResultSchemaID: string(V6ContractAtomicResultSubmission), PayloadSchemaID: "research.test.v1",
		Payload: json.RawMessage(`{"task_specific_schema":{"type":"object"}}`), Priority: 0.9, MaxAttempts: 2,
		BranchIDs: []string{uuid.NewString()},
	})
	if err != nil {
		t.Fatal(err)
	}
	err = run.store.preflightV6DirectorProposal(run.ctx, v6DirectorProposal{
		WorkspaceID: run.fixture.workspaceID, RunID: run.fixture.sessionID,
		Actions: []v6DirectorAction{{Kind: "create_work_item", Payload: payload}},
	})
	if !errors.Is(err, ErrInvalidContract) || !strings.Contains(err.Error(), "branch_id") {
		t.Fatalf("error=%v want missing Branch contract rejection", err)
	}
	var created int
	if err = run.pool.QueryRow(run.ctx, `SELECT count(*)::int FROM research_work_item
		WHERE workspace_id=$1::uuid AND session_id=$2::uuid AND kind='research'`, run.fixture.workspaceID, run.fixture.sessionID).Scan(&created); err != nil {
		t.Fatal(err)
	}
	if created != 0 {
		t.Fatalf("research Work created before Proposal preflight completed: %d", created)
	}
}
