package researchrun

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

func TestMatchDecisionAdvancesMessageVersionAndExactLineageAtomically(t *testing.T) {
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
	nodeID := uuid.NewString()

	tx, err := pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = tx.Exec(ctx, `
		INSERT INTO research_graph_node(id,workspace_id,session_id,node_type,title)
		VALUES($1::uuid,$2::uuid,$3::uuid,'finding','Finding')
	`, nodeID, fixture.workspaceID, fixture.sessionID); err != nil {
		t.Fatal(err)
	}
	if err = RegisterProductionGraphNodeTx(ctx, tx, fixture.workspaceID, fixture.sessionID, nodeID); err != nil {
		t.Fatal(err)
	}
	if _, err = tx.Exec(ctx, `
		INSERT INTO research_message(id,workspace_id,session_id,sender_type,sender_id,body)
		VALUES($1::uuid,$2::uuid,$3::uuid,'user',$4::uuid,'Question')
	`, messageID, fixture.workspaceID, fixture.sessionID, fixture.userID); err != nil {
		t.Fatal(err)
	}
	if err = RegisterProductionResearchMessageTx(ctx, tx, fixture.workspaceID, fixture.sessionID, messageID); err != nil {
		t.Fatal(err)
	}
	if err = tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}

	payload, err := json.Marshal(map[string]any{
		"utterance_id":           messageID,
		"primary_anchor_node_id": nodeID,
		"matched_node_ids":       []string{nodeID},
		"decisions":              []map[string]any{{"node_id": nodeID, "action": "continue"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	advance := func(raw []byte) error {
		tx, beginErr := pool.BeginTx(ctx, pgx.TxOptions{})
		if beginErr != nil {
			return beginErr
		}
		defer tx.Rollback(ctx)
		if advanceErr := AdvanceProductionResearchMessageMatchDecisionTx(
			ctx, tx, fixture.workspaceID, fixture.sessionID, messageID, raw,
		); advanceErr != nil {
			return advanceErr
		}
		return tx.Commit(ctx)
	}
	if err = advance(payload); err != nil {
		t.Fatal(err)
	}

	var currentVersion, referenceCount, mutationCount int
	if err = pool.QueryRow(ctx, `
		SELECT passport.current_version,
		       (SELECT count(*)::int FROM research_artifact_input_reference reference
		        JOIN research_artifact_version version ON version.id=reference.consumer_version_id
		        WHERE version.artifact_id=passport.id AND version.version=passport.current_version
		          AND reference.relation IN ('match_primary_anchor','match_candidate','match_decision')),
		       (SELECT count(*)::int FROM research_artifact_policy_mutation mutation
		        WHERE mutation.artifact_id=passport.id AND mutation.mutation_kind='current_version')
		FROM research_artifact_passport passport WHERE passport.id=$1::uuid
	`, messageID).Scan(&currentVersion, &referenceCount, &mutationCount); err != nil {
		t.Fatal(err)
	}
	if currentVersion != 2 || referenceCount != 3 || mutationCount != 1 {
		t.Fatalf("version=%d references=%d mutations=%d", currentVersion, referenceCount, mutationCount)
	}
	if err = advance(payload); err != nil {
		t.Fatalf("idempotent replay failed: %v", err)
	}
	if err = pool.QueryRow(ctx, `SELECT current_version FROM research_artifact_passport WHERE id=$1::uuid`, messageID).Scan(&currentVersion); err != nil {
		t.Fatal(err)
	}
	if currentVersion != 2 {
		t.Fatalf("idempotent replay advanced to version %d", currentVersion)
	}

	invalid, err := json.Marshal(map[string]any{
		"utterance_id": messageID, "matched_node_ids": []string{uuid.NewString()}, "decisions": []any{},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err = advance(invalid); !errors.Is(err, ErrInvalidContract) {
		t.Fatalf("invalid lineage error=%v", err)
	}
	var storedMeta []byte
	if err = pool.QueryRow(ctx, `
		SELECT message.meta,passport.current_version
		FROM research_message message JOIN research_artifact_passport passport ON passport.id=message.id
		WHERE message.id=$1::uuid
	`, messageID).Scan(&storedMeta, &currentVersion); err != nil {
		t.Fatal(err)
	}
	if currentVersion != 2 || !json.Valid(storedMeta) || string(storedMeta) != `{"match_decision": `+string(payload)+`}` {
		var stored map[string]json.RawMessage
		if json.Unmarshal(storedMeta, &stored) != nil || string(stored["match_decision"]) != string(payload) || currentVersion != 2 {
			t.Fatalf("invalid update escaped rollback: version=%d meta=%s", currentVersion, storedMeta)
		}
	}
}
