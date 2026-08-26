package researchrun

import (
	"testing"

	"github.com/google/uuid"
)

func TestBootstrapV6TransactionRecovery(t *testing.T) {
	runTransactionRecoveryMatrix(t, txOpV6Bootstrap, func(t *testing.T, run *transactionRecoveryRun) transactionRecoveryOperation {
		reporterRuntimeID := uuid.NewString()
		if _, err := run.pool.Exec(run.ctx, `
			INSERT INTO agent_runtime(id,workspace_id,name,runtime_mode,provider,status)
			VALUES($1::uuid,$2::uuid,$3,'local','pi','online')
		`, reporterRuntimeID, run.fixture.workspaceID, "reporter-template-runtime-"+reporterRuntimeID); err != nil {
			t.Fatal(err)
		}
		if _, err := run.pool.Exec(run.ctx, `
			UPDATE agent SET runtime_id=$2::uuid,model='reporter-template-model'
			WHERE id=$1::uuid
		`, run.fixture.reporterID, reporterRuntimeID); err != nil {
			t.Fatal(err)
		}
		title := "V6 bootstrap recovery " + uuid.NewString()
		input := V6BootstrapInput{
			WorkspaceID: run.fixture.workspaceID, CreatedBy: run.fixture.userID, FleetID: run.fixture.fleetID, DirectorAgentID: run.fixture.agentID, ReporterAgentID: run.fixture.reporterID,
			Goal: title, Title: title, DepthTier: "standard", Language: "English", ClientRequestID: uuid.NewString(),
		}
		invoke := func() error {
			_, _, err := run.store.BootstrapV6(run.ctx, input, DefaultRunConfig("standard"))
			return err
		}
		countSessions := func() int {
			var count int
			if err := run.pool.QueryRow(run.ctx, `
				SELECT count(*)::int
				FROM research_session
				WHERE workspace_id = $1::uuid AND title = $2 AND orchestrator_version = $3
			`, run.fixture.workspaceID, title, OrchestratorVersionV6).Scan(&count); err != nil {
				t.Fatal(err)
			}
			return count
		}
		return transactionRecoveryOperation{
			invoke: invoke,
			assertRolledBack: func() {
				if count := countSessions(); count != 0 {
					t.Fatalf("V6 session count=%d want 0", count)
				}
			},
			assertCommitted: func() {
				if count := countSessions(); count != 1 {
					t.Fatalf("V6 session count=%d want 1", count)
				}
				var branchKey string
				if err := run.pool.QueryRow(run.ctx, `
					SELECT client_key FROM research_branch
					WHERE workspace_id = $1::uuid AND session_id = (
						SELECT id FROM research_session WHERE workspace_id = $1::uuid AND title = $2 AND orchestrator_version = $3
					)
				`, run.fixture.workspaceID, title, OrchestratorVersionV6).Scan(&branchKey); err != nil {
					t.Fatal(err)
				}
				if branchKey != "root" {
					t.Fatalf("V6 root branch client_key=%q", branchKey)
				}
				var reporterAgentID, reporterRuntimeID, reporterModel, directorRuntimeID, directorModel string
				if err := run.pool.QueryRow(run.ctx, `
					SELECT reporter.id::text, reporter.runtime_id::text, COALESCE(reporter.model,''),
					       director.runtime_id::text, COALESCE(director.model,'')
					FROM research_session session
					JOIN research_team_membership membership
					  ON membership.workspace_id=session.workspace_id AND membership.session_id=session.id AND membership.role='reporter'
					JOIN agent reporter ON reporter.id=membership.agent_id
					JOIN agent director ON director.id=$4::uuid
					WHERE session.workspace_id=$1::uuid AND session.title=$2 AND session.orchestrator_version=$3
				`, run.fixture.workspaceID, title, OrchestratorVersionV6, run.fixture.agentID).Scan(
					&reporterAgentID, &reporterRuntimeID, &reporterModel, &directorRuntimeID, &directorModel,
				); err != nil {
					t.Fatal(err)
				}
				if reporterAgentID == run.fixture.reporterID {
					t.Fatal("V6 Run reused the workspace reporter template")
				}
				if reporterRuntimeID != directorRuntimeID || reporterModel != directorModel {
					t.Fatalf("Run reporter execution target=(%q,%q) want Director target=(%q,%q)", reporterRuntimeID, reporterModel, directorRuntimeID, directorModel)
				}
			},
			recover: func() error {
				if countSessions() == 1 {
					return nil
				}
				return invoke()
			},
		}
	})
}
