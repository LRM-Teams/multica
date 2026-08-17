package researchrun

import (
	"testing"

	"github.com/google/uuid"
)

func TestBootstrapV6TransactionRecovery(t *testing.T) {
	runTransactionRecoveryMatrix(t, txOpV6Bootstrap, func(t *testing.T, run *transactionRecoveryRun) transactionRecoveryOperation {
		title := "V6 bootstrap recovery " + uuid.NewString()
		input := V6BootstrapInput{
			WorkspaceID: run.fixture.workspaceID, CreatedBy: run.fixture.userID, DirectorAgentID: run.fixture.agentID,
			Goal: title, Title: title, DepthTier: "standard", Language: "English",
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
