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
	integrationAgents := map[string]bool{}
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
			if artifact.AgentID != "" {
				integrationAgents[artifact.AgentID] = true
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
	for _, dispute := range result.Disputes {
		if dispute.Subject.Kind != "question" && dispute.Subject.Kind != "hypothesis" && dispute.Subject.Kind != "claim" && dispute.Subject.Kind != "insight" {
			return fmt.Errorf("%w: Dispute subject must be Question, Hypothesis, Claim, or Insight", ErrInvalidContract)
		}
		subject, err := resolve(dispute.Subject)
		if err != nil {
			return err
		}
		disputeID := uuid.NewSHA1(namespace, []byte("integration-dispute/"+dispute.ClientKey)).String()
		severity := "advisory"
		if dispute.Materiality >= 0.75 {
			severity = "blocking"
		}
		if _, err = tx.Exec(ctx, `INSERT INTO research_dispute
			(id,workspace_id,session_id,subject_kind,subject_entity_id,dispute_kind,severity,materiality,status,resolution_request,created_by_attempt_id,client_key)
			VALUES($1::uuid,$2::uuid,$3::uuid,$4,$5::uuid,'semantic',$6,$7,'open',$8,$9::uuid,$10)`, disputeID, state.workspaceID,
			state.run.SessionID, dispute.Subject.Kind, subject.ID, severity, dispute.Materiality, strings.TrimSpace(dispute.ResolutionRequest), state.attemptID, dispute.ClientKey); err != nil {
			return err
		}
		content := map[string]any{"client_key": dispute.ClientKey, "subject_kind": dispute.Subject.Kind, "subject_entity_id": subject.ID,
			"dispute_kind": "semantic", "severity": severity, "materiality": dispute.Materiality, "status": "open",
			"resolution_request": dispute.ResolutionRequest, "positions": dispute.Positions}
		if err = registerAcceptedV6IntegrationArtifactTx(ctx, tx, state, disputeID, ArtifactKindDispute, content); err != nil {
			return err
		}
		if err = persistTypedArtifactInputReferenceTx(ctx, tx, state.workspaceID, state.run.SessionID, disputeID, ArtifactKindDispute,
			subject.ID, ArtifactEntityKind(dispute.Subject.Kind), "disputes", "v6_integration", 0); err != nil {
			return err
		}
		for ordinal, position := range dispute.Positions {
			seed, seedErr := decodeV6DisputePositionSeed(position)
			if seedErr != nil {
				return seedErr
			}
			if !integrationAgents[seed.AuthorAgentID] {
				return fmt.Errorf("%w: Dispute Position author has no accepted Integration input", ErrInvalidContract)
			}
			payload, marshalErr := json.Marshal(position)
			if marshalErr != nil {
				return marshalErr
			}
			canonical, canonicalErr := MarshalArtifactCanonicalJSON(json.RawMessage(payload))
			if canonicalErr != nil {
				return canonicalErr
			}
			positionID := uuid.NewSHA1(namespace, []byte(fmt.Sprintf("dispute-position/%s/%d", dispute.ClientKey, ordinal))).String()
			claimIDs, evidenceIDs := make([]string, 0, len(seed.ClaimRefs)), make([]string, 0, len(seed.EvidenceRefs))
			for _, ref := range seed.ClaimRefs {
				id, resolveErr := resolveV6EntityKeyTx(ctx, tx, state, ref)
				if resolveErr != nil {
					return resolveErr
				}
				claimIDs = append(claimIDs, id)
			}
			for _, ref := range seed.EvidenceRefs {
				id, resolveErr := resolveV6EntityKeyTx(ctx, tx, state, ref)
				if resolveErr != nil {
					return resolveErr
				}
				evidenceIDs = append(evidenceIDs, id)
			}
			encodedClaims, _ := json.Marshal(claimIDs)
			encodedEvidence, _ := json.Marshal(evidenceIDs)
			positionScope, _ := json.Marshal(seed.Scope)
			if _, err = tx.Exec(ctx, `INSERT INTO research_dispute_position
				(id,workspace_id,session_id,dispute_id,author_agent_id,statement,scope,claim_ids,evidence_ids,position_payload)
				VALUES($1::uuid,$2::uuid,$3::uuid,$4::uuid,$5::uuid,$6,$7::jsonb,$8::jsonb,$9::jsonb,$10::jsonb)`, positionID,
				state.workspaceID, state.run.SessionID, disputeID, seed.AuthorAgentID, seed.Statement, positionScope, encodedClaims, encodedEvidence, payload); err != nil {
				return err
			}
			positionContent := map[string]any{"dispute_id": disputeID, "author_agent_id": seed.AuthorAgentID, "statement": seed.Statement,
				"scope": seed.Scope, "claim_ids": claimIDs, "evidence_ids": evidenceIDs, "position_payload": json.RawMessage(payload), "ordinal": ordinal,
				"canonical_payload": string(canonical)}
			if err = registerAcceptedV6IntegrationArtifactTx(ctx, tx, state, positionID, ArtifactKindDisputePosition, positionContent); err != nil {
				return err
			}
			if err = persistTypedArtifactInputReferenceTx(ctx, tx, state.workspaceID, state.run.SessionID, positionID, ArtifactKindDisputePosition,
				disputeID, ArtifactKindDispute, "position_on", "v6_integration", 0); err != nil {
				return err
			}
		}
	}
	if err := applyAcceptedV6StatusUpdatesTx(ctx, tx, state, result.StatusUpdates, agentID); err != nil {
		return err
	}
	followUpQuestions := make([]QuestionProposal, 0)
	for _, contribution := range result.IntegrationContributions {
		for _, question := range contribution.FollowUpQuestions {
			parent := ""
			if question.ParentClientKey != nil {
				parent = *question.ParentClientKey
			}
			followUpQuestions = append(followUpQuestions, QuestionProposal{ClientKey: question.ClientKey, ParentClientKey: parent,
				Kind: QuestionKind(question.Kind), Text: question.Text, Required: question.Required, Priority: question.Priority,
				Impact: question.Impact, Uncertainty: question.Uncertainty, Novelty: question.Novelty})
		}
	}
	questionIDs, _, err := materializeQuestions(ctx, tx, state, ResultEnvelope{Questions: followUpQuestions})
	if err != nil {
		return err
	}
	taskProposals := make([]TaskProposal, 0, len(result.ProposedTasks))
	for _, task := range result.ProposedTasks {
		acceptanceCriteria := task.AcceptanceCriteria
		if acceptanceCriteria == nil {
			acceptanceCriteria = map[string]any{}
		}
		criteria, marshalErr := json.Marshal(acceptanceCriteria)
		if marshalErr != nil {
			return marshalErr
		}
		questionKey := ""
		for _, target := range task.Targets {
			if target.Kind == "question" && questionKey == "" {
				questionKey = target.Key
			}
		}
		maxAttempts, timeout := 0, 0
		if task.MaxAttempts != nil {
			maxAttempts = *task.MaxAttempts
		}
		if task.TimeoutSeconds != nil {
			timeout = *task.TimeoutSeconds
		}
		taskProposals = append(taskProposals, TaskProposal{ClientKey: task.ClientKey, QuestionKey: questionKey, Kind: TaskKind(task.Kind),
			Objective: task.Objective, RequiredCapability: task.RequiredCapability, ExpectedResult: task.ExpectedResult,
			AcceptanceCriteria: criteria, Priority: task.Priority, DependsOn: task.DependsOn, MaxAttempts: maxAttempts, TimeoutSeconds: timeout})
	}
	if _, err = materializeTasks(ctx, tx, state, ResultEnvelope{ProposedTasks: taskProposals}, questionIDs); err != nil {
		return err
	}
	if len(result.ProposedTasks) > 0 {
		taskIDs, loadErr := loadTaskIDs(ctx, tx, state.run.SessionID, state.run.GoalVersion, state.targetPlan)
		if loadErr != nil {
			return loadErr
		}
		for _, task := range result.ProposedTasks {
			taskID := taskIDs[task.ClientKey]
			if taskID == "" {
				return fmt.Errorf("%w: materialized V6 follow-up Task cannot be resolved", ErrResultConflict)
			}
			if _, err = tx.Exec(ctx, `INSERT INTO research_task_dependency(workspace_id,session_id,task_id,depends_on_task_id)
				VALUES($1::uuid,$2::uuid,$3::uuid,$4::uuid) ON CONFLICT DO NOTHING`, state.workspaceID, state.run.SessionID, taskID, state.task.ID); err != nil {
				return err
			}
			for ordinal, target := range task.Targets {
				if target.Kind == "task" || target.Kind == "source" {
					return fmt.Errorf("%w: follow-up Task target must be an Inquiry entity", ErrInvalidContract)
				}
				targetID, resolveErr := resolveV6EntityKeyTx(ctx, tx, state, target)
				if resolveErr != nil {
					return resolveErr
				}
				if _, err = tx.Exec(ctx, `INSERT INTO research_task_inquiry_target
					(workspace_id,session_id,task_id,target_kind,target_entity_id,goal_version,plan_version,bound_by_attempt_id)
					VALUES($1::uuid,$2::uuid,$3::uuid,$4,$5::uuid,$6,$7,$8::uuid) ON CONFLICT DO NOTHING`, state.workspaceID,
					state.run.SessionID, taskID, target.Kind, targetID, state.run.GoalVersion, state.targetPlan, state.attemptID); err != nil {
					return err
				}
				if err = persistTypedArtifactInputReferenceTx(ctx, tx, state.workspaceID, state.run.SessionID, taskID, ArtifactKindTask,
					targetID, ArtifactEntityKind(target.Kind), "targets", "v6_integration_follow_up", ordinal); err != nil {
					return err
				}
			}
			if err = persistMaterializedTaskRelationshipReferencesTx(ctx, tx, state.workspaceID, state.run.SessionID, taskID); err != nil {
				return err
			}
		}
	}
	if _, err := tx.Exec(ctx, `UPDATE research_integration_round SET status='accepted',updated_at=now() WHERE workspace_id=$1::uuid AND session_id=$2::uuid AND id=$3::uuid`, state.workspaceID, state.run.SessionID, roundID); err != nil {
		return err
	}
	_, err = appendEvent(ctx, tx, state.workspaceID, state.run.SessionID, "v6_integration_materialized", "v6-integration:"+state.attemptID, "agent", agentID,
		map[string]any{"attempt_id": state.attemptID, "integration_round_id": roundID, "contributions": len(result.IntegrationContributions),
			"insights": len(result.Insights), "disputes": len(result.Disputes), "follow_up_questions": len(followUpQuestions), "follow_up_tasks": len(result.ProposedTasks)})
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
