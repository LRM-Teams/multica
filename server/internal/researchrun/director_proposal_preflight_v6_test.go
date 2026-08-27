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
			facts:   v6DirectorPreflightFacts{maxParallelTasks: 5, proposedAgentCount: 1, proposedBranches: 3},
			wantErr: "至少需要 3 个不同的 run-scoped Agent",
		},
		{
			name:  "three initial Agents satisfy staffing",
			facts: v6DirectorPreflightFacts{maxParallelTasks: 5, proposedAgentCount: 3, proposedBranches: 3},
		},
		{
			name:    "initial staffing must create child Branches",
			facts:   v6DirectorPreflightFacts{maxParallelTasks: 5, proposedAgentCount: 3},
			wantErr: "至少 3 个非根子 Branch",
		},
		{
			name:    "one broad Work is not parallel research",
			facts:   v6DirectorPreflightFacts{maxParallelTasks: 5, workerCount: 3, proposedAtomicWork: 1, proposedWorkBranches: 1, childBranches: 3},
			wantErr: "至少 3 个不同的子 Branch",
		},
		{
			name:  "three independent Work Items satisfy first round",
			facts: v6DirectorPreflightFacts{maxParallelTasks: 5, workerCount: 3, proposedAtomicWork: 3, proposedWorkBranches: 3, childBranches: 3},
		},
		{
			name:    "early research still fills the minimum parallel frontier",
			facts:   v6DirectorPreflightFacts{maxParallelTasks: 5, workerCount: 3, resultCount: 1, unresolvedQuestions: 3, proposedAtomicWork: 1, proposedWorkBranches: 1, childBranches: 3},
			wantErr: "至少需要 2 个分配给不同 Agent 的 atomic Work",
		},
		{
			name: "late follow-up may make partial parallel progress",
			facts: v6DirectorPreflightFacts{maxParallelTasks: 5, workerCount: 5, resultCount: 10,
				unresolvedQuestions: 8, proposedAtomicWork: 3, proposedWorkBranches: 3, childBranches: 5},
		},
		{
			name:    "late follow-up cannot ignore all unresolved questions",
			facts:   v6DirectorPreflightFacts{maxParallelTasks: 5, workerCount: 5, resultCount: 10, unresolvedQuestions: 8, childBranches: 5},
			wantErr: "至少需要 1 个分配给不同 Agent 的 atomic Work",
		},
		{
			name:  "pending Agent creation permits a staffing cycle",
			facts: v6DirectorPreflightFacts{maxParallelTasks: 5, workerCount: 1, pendingAgentCount: 2, childBranches: 3},
		},
		{
			name: "report-only maintenance cycle does not starve behind open questions",
			facts: v6DirectorPreflightFacts{maxParallelTasks: 5, workerCount: 5, resultCount: 10,
				unresolvedQuestions: 12, proposedReports: 1, hasReport: true, childBranches: 5},
		},
		{
			name: "report refresh can make progress with partial parallel follow-up",
			facts: v6DirectorPreflightFacts{maxParallelTasks: 5, workerCount: 5, resultCount: 10,
				unresolvedQuestions: 12, proposedAtomicWork: 3, proposedWorkBranches: 3,
				proposedReports: 1, hasReport: true, childBranches: 5},
		},
		{
			name: "surplus Agent creation does not block work assigned to joined workers",
			facts: v6DirectorPreflightFacts{maxParallelTasks: 5, workerCount: 3, proposedAgentCount: 1,
				resultCount: 3, unresolvedQuestions: 3, proposedAtomicWork: 3, proposedWorkBranches: 3, childBranches: 3},
		},
		{
			name: "required Agent creation cannot be mixed with premature work",
			facts: v6DirectorPreflightFacts{maxParallelTasks: 5, proposedAgentCount: 1,
				resultCount: 3, unresolvedQuestions: 3, proposedAtomicWork: 1, proposedWorkBranches: 1, childBranches: 3},
			wantErr: "Agent 创建是异步的",
		},
		{
			name: "convergence may consume unresolved frontier nodes before more research",
			facts: v6DirectorPreflightFacts{maxParallelTasks: 5, workerCount: 3, resultCount: 2,
				unresolvedQuestions: 4, proposedConvergence: 1, convergenceReady: true},
		},
		{
			name:    "atomic Work cannot use only the bootstrap root Branch",
			facts:   v6DirectorPreflightFacts{maxParallelTasks: 5, workerCount: 3, proposedAtomicWork: 3},
			wantErr: "创建子 Branch",
		},
		{
			name: "same-tier frontier must converge before more accumulation",
			facts: v6DirectorPreflightFacts{maxParallelTasks: 5, workerCount: 3, childBranches: 3,
				resultCount: 2, convergenceReady: true},
			wantErr: "必须先创建 integration Discussion",
		},
		{
			name: "same-tier convergence cannot be mixed with more atomic accumulation",
			facts: v6DirectorPreflightFacts{maxParallelTasks: 5, workerCount: 3, childBranches: 3,
				resultCount: 2, convergenceReady: true, proposedConvergence: 1, proposedAtomicWork: 1, proposedWorkBranches: 1},
			wantErr: "不能同时继续堆积 atomic Work",
		},
		{
			name: "escalated convergence permits independent evidence follow-up",
			facts: v6DirectorPreflightFacts{maxParallelTasks: 5, workerCount: 3, childBranches: 3,
				resultCount: 2, unresolvedQuestions: 1, convergenceReady: true, openConvergence: 1,
				proposedAtomicWork: 1, proposedWorkBranches: 1},
		},
		{
			name:    "top-level directions are bounded by parallel capacity",
			facts:   v6DirectorPreflightFacts{maxParallelTasks: 5, topLevelBranches: 5, proposedTopLevel: 1},
			wantErr: "一级研究方向最多 5 个",
		},
		{
			name:  "nested Branch does not consume another top-level direction",
			facts: v6DirectorPreflightFacts{maxParallelTasks: 5, topLevelBranches: 5},
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

func TestRequiredV6FollowupCountDoesNotDoubleCountEscalatedDiscussionQuestions(t *testing.T) {
	tests := []struct {
		name                 string
		openQuestions        int
		escalatedDiscussions int
		want                 int
	}{
		{name: "frontier questions cover one escalated discussion", openQuestions: 3, escalatedDiscussions: 1, want: 3},
		{name: "escalation still requires follow-up without frontier questions", escalatedDiscussions: 1, want: 1},
		{name: "distinct escalations set the minimum", openQuestions: 1, escalatedDiscussions: 2, want: 2},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := requiredV6FollowupCount(tt.openQuestions, tt.escalatedDiscussions); got != tt.want {
				t.Fatalf("requiredV6FollowupCount(%d, %d)=%d, want %d", tt.openQuestions, tt.escalatedDiscussions, got, tt.want)
			}
		})
	}
}

func TestValidateV6IntegrationCandidateRequiresExecutableTierTransition(t *testing.T) {
	result := func(id string, tier V6Tier) V6NodeRef {
		return V6NodeRef{Kind: "result_s", ID: uuid.NewString(), VersionID: id, Tier: tier, ContentHash: "sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"}
	}
	tests := []struct {
		name    string
		inputs  []V6NodeRef
		wantErr bool
	}{
		{name: "two S nodes can promote to M", inputs: []V6NodeRef{result(uuid.NewString(), V6TierS), result(uuid.NewString(), V6TierS)}},
		{name: "one M can assimilate one S", inputs: []V6NodeRef{result(uuid.NewString(), V6TierM), result(uuid.NewString(), V6TierS)}},
		{name: "mixed promotion and assimilation set is impossible", inputs: []V6NodeRef{result(uuid.NewString(), V6TierM), result(uuid.NewString(), V6TierM), result(uuid.NewString(), V6TierS)}, wantErr: true},
		{name: "duplicate node version cannot satisfy input cardinality", inputs: func() []V6NodeRef {
			versionID := uuid.NewString()
			return []V6NodeRef{result(versionID, V6TierS), result(versionID, V6TierS)}
		}(), wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validateV6IntegrationCandidate(tt.inputs)
			if tt.wantErr && !errors.Is(err, ErrV6InvalidTierTransition) {
				t.Fatalf("error=%v want ErrV6InvalidTierTransition", err)
			}
			if !tt.wantErr && err != nil {
				t.Fatalf("validate integration candidate: %v", err)
			}
		})
	}
}

func TestValidateV6IntegrationDiscussionOutcome(t *testing.T) {
	if err := validateV6IntegrationDiscussionOutcome(V6Discussion{Status: "active"}); err != nil {
		t.Fatalf("active discussion should be accepted: %v", err)
	}
	for _, status := range []string{"consensus_accept", "consensus_reject", "escalated"} {
		err := validateV6IntegrationDiscussionOutcome(V6Discussion{Status: status})
		if !errors.Is(err, ErrInvalidContract) {
			t.Fatalf("status %q error=%v want ErrInvalidContract", status, err)
		}
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
