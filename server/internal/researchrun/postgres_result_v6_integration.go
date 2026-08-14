package researchrun

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

type resolvedIntegrationArtifact struct {
	ID, Kind, Hash, AgentID string
	Level                   int
}

func materializeAcceptedV6IntegrationTx(ctx context.Context, tx pgx.Tx, state acceptedResultState, result V6IntegrationResult, agentID string) error {
	if state.stale {
		return fmt.Errorf("%w: stale Integration Result cannot mutate canonical state", ErrInvalidTransition)
	}
	roundID := result.IntegrationContributions[0].IntegrationRoundID
	for _, contribution := range result.IntegrationContributions {
		if contribution.IntegrationRoundID != roundID {
			return fmt.Errorf("%w: Integration Result spans multiple rounds", ErrInvalidResult)
		}
	}
	var roundStatus string
	var roundTaskID *string
	if err := tx.QueryRow(ctx, `SELECT status,created_by_task_id::text FROM research_integration_round
		WHERE workspace_id=$1::uuid AND session_id=$2::uuid AND id=$3::uuid FOR UPDATE`, state.workspaceID, state.run.SessionID, roundID).Scan(&roundStatus, &roundTaskID); err != nil {
		if err == pgx.ErrNoRows {
			return fmt.Errorf("%w: Integration Round is outside this Run", ErrInvalidContract)
		}
		return err
	}
	if roundTaskID == nil || *roundTaskID != state.task.ID || (roundStatus != "pending" && roundStatus != "running") {
		return fmt.Errorf("%w: Integration Round is not assigned to this active Task", ErrInvalidTransition)
	}

	resolved := map[V6EntityRef]resolvedIntegrationArtifact{}
	resolve := func(ref V6EntityRef) (resolvedIntegrationArtifact, error) {
		if artifact, ok := resolved[ref]; ok {
			return artifact, nil
		}
		id, err := resolveV6EntityKeyTx(ctx, tx, state, ref)
		if err != nil {
			return resolvedIntegrationArtifact{}, err
		}
		var artifact resolvedIntegrationArtifact
		artifact.ID, artifact.Kind = id, ref.Kind
		err = tx.QueryRow(ctx, `SELECT version.content_hash,COALESCE(version.produced_by_agent_id::text,attempt.assigned_agent_id::text,'')
			FROM research_artifact_passport passport
			JOIN research_artifact_version version ON (version.workspace_id,version.session_id,version.artifact_id,version.version)=(passport.workspace_id,passport.session_id,passport.id,passport.current_version)
			LEFT JOIN research_task_attempt attempt ON (attempt.workspace_id,attempt.session_id,attempt.id)=(version.workspace_id,version.session_id,version.produced_by_attempt_id)
			WHERE passport.workspace_id=$1::uuid AND passport.session_id=$2::uuid AND passport.id=$3::uuid
			AND passport.lifecycle_status IN ('registered','accepted')`, state.workspaceID, state.run.SessionID, id).Scan(&artifact.Hash, &artifact.AgentID)
		if err != nil {
			if err == pgx.ErrNoRows {
				return resolvedIntegrationArtifact{}, fmt.Errorf("%w: Integration input has no admissible current Passport version", ErrInvalidContract)
			}
			return resolvedIntegrationArtifact{}, err
		}
		if ref.Kind == "insight" {
			if err = tx.QueryRow(ctx, `SELECT level FROM research_insight WHERE workspace_id=$1::uuid AND session_id=$2::uuid AND id=$3::uuid`, state.workspaceID, state.run.SessionID, id).Scan(&artifact.Level); err != nil {
				return resolvedIntegrationArtifact{}, err
			}
		}
		resolved[ref] = artifact
		return artifact, nil
	}

	namespace := uuid.MustParse(state.attemptID)
	for _, contribution := range result.IntegrationContributions {
		ids := make([]string, 0, len(contribution.ComparedArtifacts))
		authorID := ""
		for _, ref := range contribution.ComparedArtifacts {
			artifact, err := resolve(ref)
			if err != nil {
				return err
			}
			ids = append(ids, artifact.ID)
			if authorID == "" && artifact.AgentID != "" {
				authorID = artifact.AgentID
			}
		}
		if authorID == "" {
			return fmt.Errorf("%w: Integration Contribution has no original Agent author", ErrInvalidContract)
		}
		id := uuid.NewSHA1(namespace, []byte("integration-contribution/"+contribution.ClientKey)).String()
		compared, _ := json.Marshal(ids)
		common, _ := json.Marshal(contribution.CommonFindings)
		unique, _ := json.Marshal(contribution.UniqueFindings)
		conflicts, _ := json.Marshal(contribution.Conflicts)
		scope, _ := json.Marshal(contribution.Scope)
		omissions, _ := json.Marshal(contribution.Omissions)
		if _, err := tx.Exec(ctx, `INSERT INTO research_integration_contribution
			(id,workspace_id,session_id,integration_round_id,author_agent_id,compared_artifact_ids,common_findings,unique_findings,conflicts,scope,omissions)
			VALUES($1::uuid,$2::uuid,$3::uuid,$4::uuid,$5::uuid,$6::jsonb,$7::jsonb,$8::jsonb,$9::jsonb,$10::jsonb,$11::jsonb)`,
			id, state.workspaceID, state.run.SessionID, roundID, authorID, compared, common, unique, conflicts, scope, omissions); err != nil {
			return err
		}
		content := map[string]any{"integration_round_id": roundID, "author_agent_id": authorID, "compared_artifact_ids": ids,
			"common_findings": contribution.CommonFindings, "unique_findings": contribution.UniqueFindings, "conflicts": contribution.Conflicts,
			"scope": contribution.Scope, "omissions": contribution.Omissions, "proposed_insights": contribution.ProposedInsights,
			"follow_up_questions": contribution.FollowUpQuestions}
		if err := registerAcceptedV6IntegrationArtifactTx(ctx, tx, state, id, ArtifactKindIntegrationContribution, content); err != nil {
			return err
		}
		for ordinal, inputID := range ids {
			if err := persistTypedArtifactInputReferenceTx(ctx, tx, state.workspaceID, state.run.SessionID, id, ArtifactKindIntegrationContribution,
				inputID, ArtifactEntityKind(contribution.ComparedArtifacts[ordinal].Kind), "compares", "v6_integration", ordinal); err != nil {
				return err
			}
		}
	}

	for _, insight := range result.Insights {
		id := uuid.NewSHA1(namespace, []byte("integration-insight/"+insight.ClientKey)).String()
		level := 1
		inputs := make([]resolvedIntegrationArtifact, 0, len(insight.Inputs))
		for _, ref := range insight.Inputs {
			if ref.Kind != "claim" && ref.Kind != "insight" {
				return fmt.Errorf("%w: Insight derivation input must be Claim or Insight", ErrInvalidContract)
			}
			artifact, err := resolve(ref)
			if err != nil {
				return err
			}
			if artifact.Level+1 > level {
				level = artifact.Level + 1
			}
			inputs = append(inputs, artifact)
		}
		createdAt := time.Now().UTC()
		if _, err := tx.Exec(ctx, `INSERT INTO research_insight(id,workspace_id,session_id,title,summary,status,importance,level,created_by_attempt_id,created_at,updated_at,client_key)
			VALUES($1::uuid,$2::uuid,$3::uuid,$4,$5,'accepted',$6,$7,$8::uuid,$9,$9,$10)`, id, state.workspaceID, state.run.SessionID,
			strings.TrimSpace(insight.Title), strings.TrimSpace(insight.Summary), result.Confidence, level, state.attemptID, createdAt, insight.ClientKey); err != nil {
			return err
		}
		content := map[string]any{"title": insight.Title, "summary": insight.Summary, "status": "accepted", "importance": result.Confidence, "level": level,
			"relation": insight.Relation, "scope": insight.Scope, "semantic_value": insight.SemanticValue}
		if err := registerAcceptedV6IntegrationArtifactTx(ctx, tx, state, id, ArtifactKindInsight, content); err != nil {
			return err
		}
		scopeJSON, _ := json.Marshal(insight.Scope)
		scopeCanonical, err := MarshalArtifactCanonicalJSON(json.RawMessage(scopeJSON))
		if err != nil {
			return err
		}
		scopeHash := ArtifactContentHashFromCanonicalJSON(scopeCanonical)
		for ordinal, input := range inputs {
			derivationID := uuid.NewSHA1(namespace, []byte(fmt.Sprintf("insight-derivation/%s/%d", insight.ClientKey, ordinal))).String()
			if _, err = tx.Exec(ctx, `INSERT INTO research_insight_derivation(id,workspace_id,session_id,insight_id,input_kind,input_entity_id,input_content_hash,scope_hash,relation)
				VALUES($1::uuid,$2::uuid,$3::uuid,$4::uuid,$5,$6::uuid,$7,$8,$9)`, derivationID, state.workspaceID, state.run.SessionID,
				id, input.Kind, input.ID, input.Hash, scopeHash, insight.Relation); err != nil {
				return err
			}
			if err = persistTypedArtifactInputReferenceTx(ctx, tx, state.workspaceID, state.run.SessionID, id, ArtifactKindInsight,
				input.ID, ArtifactEntityKind(input.Kind), insight.Relation, "v6_integration", ordinal); err != nil {
				return err
			}
		}
	}
	if err := applyAcceptedV6StatusUpdatesTx(ctx, tx, state, result.StatusUpdates, agentID); err != nil {
		return err
	}
	if len(result.Disputes) > 0 || len(result.ProposedTasks) > 0 {
		return fmt.Errorf("%w: V6 Integration Dispute and follow-up Task persistence is not available", ErrUnsupportedVersion)
	}
	if _, err := tx.Exec(ctx, `UPDATE research_integration_round SET status='accepted',updated_at=now() WHERE workspace_id=$1::uuid AND session_id=$2::uuid AND id=$3::uuid`, state.workspaceID, state.run.SessionID, roundID); err != nil {
		return err
	}
	_, err := appendEvent(ctx, tx, state.workspaceID, state.run.SessionID, "v6_integration_materialized", "v6-integration:"+state.attemptID, "agent", agentID,
		map[string]any{"attempt_id": state.attemptID, "integration_round_id": roundID, "contributions": len(result.IntegrationContributions), "insights": len(result.Insights)})
	return err
}

func registerAcceptedV6IntegrationArtifactTx(ctx context.Context, tx pgx.Tx, state acceptedResultState, entityID string, kind ArtifactEntityKind, content map[string]any) error {
	hash, err := ArtifactContentHash(kind, content)
	if err != nil {
		return err
	}
	goal, plan := int32(state.run.GoalVersion), int32(state.targetPlan)
	return registerArtifactPassportTx(ctx, tx, registerArtifactPassportInput{WorkspaceID: state.workspaceID, SessionID: state.run.SessionID,
		EntityID: entityID, Kind: kind, ProvenanceCompleteness: ArtifactProvenanceComplete, GoalVersion: &goal, PlanVersion: &plan,
		AccessLevel: state.outputAccess, HashOrigin: ArtifactHashOriginProduction, ContentHash: hash, ProducedByTaskID: state.task.ID,
		ProducedByAttemptID: state.attemptID, SchemaName: string(kind), SchemaVersion: OrchestratorVersionV6})
}
