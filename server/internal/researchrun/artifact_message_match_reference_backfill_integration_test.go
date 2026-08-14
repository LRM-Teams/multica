package researchrun

import (
	"context"
	"encoding/json"
	"os"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

func TestMessageMatchReferenceBackfillDiagnosesRepairsAndPreservesOrdinals(t *testing.T) {
	databaseURL := os.Getenv("TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("TEST_DATABASE_URL is not set")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	pool, err := pgxpool.New(ctx, databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	fixture := seedResearchRunFixture(t, ctx, pool)
	messageID := uuid.NewString()
	firstNode := uuid.NewString()
	secondNode := uuid.NewString()

	tx, err := pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		t.Fatal(err)
	}
	for _, node := range []string{firstNode, secondNode} {
		if _, err = tx.Exec(ctx, `
			INSERT INTO research_graph_node(id,workspace_id,session_id,node_type,title)
			VALUES($1::uuid,$2::uuid,$3::uuid,'finding','Finding')
		`, node, fixture.workspaceID, fixture.sessionID); err != nil {
			t.Fatal(err)
		}
		if err = RegisterProductionGraphNodeTx(ctx, tx, fixture.workspaceID, fixture.sessionID, node); err != nil {
			t.Fatal(err)
		}
	}
	meta, _ := json.Marshal(map[string]any{"match_decision": map[string]any{
		"utterance_id": messageID, "primary_anchor_node_id": firstNode,
		"matched_node_ids": []string{secondNode, firstNode},
		"decisions":        []map[string]any{{"node_id": secondNode, "action": "continue"}},
	}})
	if _, err = tx.Exec(ctx, `
		INSERT INTO research_message(id,workspace_id,session_id,sender_type,sender_id,body,meta)
		VALUES($1::uuid,$2::uuid,$3::uuid,'user',$4::uuid,'Question',$5::jsonb)
	`, messageID, fixture.workspaceID, fixture.sessionID, fixture.userID, meta); err != nil {
		t.Fatal(err)
	}
	if err = RegisterProductionResearchMessageTx(ctx, tx, fixture.workspaceID, fixture.sessionID, messageID); err != nil {
		t.Fatal(err)
	}
	if err = tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}

	if _, err = pool.Exec(ctx, `SELECT research_artifact_materialize_message_match_references($1::uuid,$2::uuid,$3::uuid)`, fixture.workspaceID, fixture.sessionID, messageID); err != nil {
		t.Fatal(err)
	}
	rows, err := pool.Query(ctx, `
		SELECT reference.relation,reference.ordinal
		FROM research_artifact_input_reference reference
		JOIN research_artifact_version consumer ON consumer.id=reference.consumer_version_id
		WHERE consumer.artifact_id=$1::uuid AND reference.purpose='match_decision_migration'
		ORDER BY reference.relation,reference.ordinal
	`, messageID)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	got := map[string][]int{}
	for rows.Next() {
		var relation string
		var ordinal int
		if err = rows.Scan(&relation, &ordinal); err != nil {
			t.Fatal(err)
		}
		got[relation] = append(got[relation], ordinal)
	}
	if len(got["match_primary_anchor"]) != 1 || len(got["match_candidate"]) != 2 ||
		got["match_candidate"][0] != 0 || got["match_candidate"][1] != 1 || len(got["match_decision"]) != 1 {
		t.Fatalf("reference ordinals=%v", got)
	}

	duplicate := map[string]any{"match_decision": map[string]any{
		"utterance_id": messageID, "matched_node_ids": []string{firstNode, firstNode}, "decisions": []any{},
	}}
	if _, err = pool.Exec(ctx, `UPDATE research_message SET meta=$2::jsonb WHERE id=$1::uuid`, messageID, mustJSON(t, duplicate)); err != nil {
		t.Fatal(err)
	}
	if _, err = pool.Exec(ctx, `SELECT research_artifact_materialize_message_match_references($1::uuid,$2::uuid,$3::uuid)`, fixture.workspaceID, fixture.sessionID, messageID); err != nil {
		t.Fatal(err)
	}
	assertMessageMatchBackfillDiagnostic(t, ctx, pool, messageID, "/meta/match_decision/matched_node_ids/1", "duplicate_local_key")

	repaired := map[string]any{"match_decision": map[string]any{
		"utterance_id": messageID, "matched_node_ids": []string{firstNode}, "decisions": []any{},
	}}
	if _, err = pool.Exec(ctx, `UPDATE research_message SET meta=$2::jsonb WHERE id=$1::uuid`, messageID, mustJSON(t, repaired)); err != nil {
		t.Fatal(err)
	}
	if _, err = pool.Exec(ctx, `SELECT research_artifact_materialize_message_match_references($1::uuid,$2::uuid,$3::uuid)`, fixture.workspaceID, fixture.sessionID, messageID); err != nil {
		t.Fatal(err)
	}
	var count int
	if err = pool.QueryRow(ctx, `SELECT count(*)::int FROM research_artifact_migration_diagnostic WHERE owner_kind='research_message' AND owner_id=$1::uuid`, messageID).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 0 {
		t.Fatalf("repair left %d diagnostics", count)
	}
	if _, err = pool.Exec(ctx, `
		UPDATE research_artifact_input_reference SET purpose='match_decision'
		WHERE purpose='match_decision_migration' AND consumer_version_id IN (
		  SELECT id FROM research_artifact_version WHERE artifact_id=$1::uuid
		)
	`, messageID); err != nil {
		t.Fatal(err)
	}
	if _, err = pool.Exec(ctx, `SELECT research_artifact_materialize_message_match_references($1::uuid,$2::uuid,$3::uuid)`, fixture.workspaceID, fixture.sessionID, messageID); err != nil {
		t.Fatal(err)
	}
	if err = pool.QueryRow(ctx, `
		SELECT count(*)::int FROM research_artifact_input_reference reference
		JOIN research_artifact_version version ON version.id=reference.consumer_version_id
		WHERE version.artifact_id=$1::uuid AND reference.purpose='match_decision_migration'
	`, messageID).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 0 {
		t.Fatalf("rescan replaced %d production references", count)
	}
}

func mustJSON(t *testing.T, value any) []byte {
	t.Helper()
	raw, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

func assertMessageMatchBackfillDiagnostic(t *testing.T, ctx context.Context, pool *pgxpool.Pool, messageID, path, reason string) {
	t.Helper()
	var got string
	if err := pool.QueryRow(ctx, `SELECT reason_code FROM research_artifact_migration_diagnostic WHERE owner_kind='research_message' AND owner_id=$1::uuid AND field_path=$2`, messageID, path).Scan(&got); err != nil {
		t.Fatal(err)
	}
	if got != reason {
		t.Fatalf("diagnostic=%q want=%q", got, reason)
	}
}
