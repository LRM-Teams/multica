package researchrun

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// RegisterProductionResearchMessageTx registers a newly persisted Research
// Message in the caller's transaction. Both HTTP handlers and the chat mirror
// use this boundary so a domain row can never commit with migration provenance.
func RegisterProductionResearchMessageTx(
	ctx context.Context,
	tx pgx.Tx,
	workspaceID, sessionID, messageID string,
) error {
	var senderType, senderID, targetAgentID, runEventID, body, cardKind string
	var meta []byte
	var createdAt time.Time
	err := tx.QueryRow(ctx, `
		SELECT sender_type, COALESCE(sender_id::text, ''), COALESCE(target_agent_id::text, ''),
		       COALESCE(run_event_id::text, ''), body, card_kind, meta, created_at
		FROM research_message
		WHERE workspace_id = $1::uuid AND session_id = $2::uuid AND id = $3::uuid
	`, workspaceID, sessionID, messageID).Scan(
		&senderType, &senderID, &targetAgentID, &runEventID, &body, &cardKind, &meta, &createdAt,
	)
	if err != nil {
		return err
	}
	contentHash, err := ArtifactContentHash(ArtifactKindResearchMessage, researchMessageArtifactContent(
		senderType, senderID, targetAgentID, runEventID, body, cardKind, meta,
	))
	if err != nil {
		return err
	}
	return registerArtifactPassportTx(ctx, tx, registerArtifactPassportInput{
		WorkspaceID: workspaceID, SessionID: sessionID, EntityID: messageID,
		Kind: ArtifactKindResearchMessage, SourceCreatedAt: &createdAt,
		ProvenanceCompleteness: ArtifactProvenanceComplete,
		AccessLevel:            ArtifactAccessRaw, HashOrigin: ArtifactHashOriginProduction,
		ContentHash: contentHash,
	})
}

func researchMessageArtifactContent(senderType, senderID, targetAgentID, runEventID, body, cardKind string, meta []byte) map[string]any {
	content := map[string]any{
		"sender_type":     senderType,
		"sender_id":       senderID,
		"target_agent_id": targetAgentID,
		"body":            body,
		"card_kind":       cardKind,
		"meta":            json.RawMessage(meta),
	}
	if runEventID != "" {
		content["run_event_id"] = runEventID
	}
	return content
}

type researchMessageMatchDecisionRefs struct {
	UtteranceID       string   `json:"utterance_id"`
	PrimaryAnchorNode string   `json:"primary_anchor_node_id"`
	MatchedNodeIDs    []string `json:"matched_node_ids"`
	Decisions         []struct {
		NodeID string `json:"node_id"`
	} `json:"decisions"`
}

type researchMessageInputRef struct {
	ArtifactID string
	Kind       ArtifactEntityKind
	Relation   string
	Ordinal    int
}

// AdvanceProductionResearchMessageMatchDecisionTx commits a Message semantic
// change, its immutable version, reciprocal policy mutation, and typed lineage
// in one caller-owned transaction.
func AdvanceProductionResearchMessageMatchDecisionTx(
	ctx context.Context,
	tx pgx.Tx,
	workspaceID, sessionID, messageID string,
	matchDecision json.RawMessage,
) error {
	var senderType, senderID, targetAgentID, runEventID, body, cardKind string
	var meta []byte
	err := tx.QueryRow(ctx, `
		SELECT sender_type,COALESCE(sender_id::text,''),COALESCE(target_agent_id::text,''),
		       COALESCE(run_event_id::text,''),body,card_kind,meta
		FROM research_message
		WHERE workspace_id=$1::uuid AND session_id=$2::uuid AND id=$3::uuid
		FOR UPDATE
	`, workspaceID, sessionID, messageID).Scan(
		&senderType, &senderID, &targetAgentID, &runEventID, &body, &cardKind, &meta,
	)
	if err != nil {
		return err
	}
	if senderType != "user" {
		return fmt.Errorf("%w: match decision requires a user Message", ErrInvalidContract)
	}
	refs, err := parseResearchMessageMatchDecisionRefs(messageID, matchDecision)
	if err != nil {
		return err
	}
	var updatedMeta []byte
	if err = tx.QueryRow(ctx, `
		UPDATE research_message
		SET meta=jsonb_set(COALESCE(meta,'{}'::jsonb),'{match_decision}',$4::jsonb,true)
		WHERE workspace_id=$1::uuid AND session_id=$2::uuid AND id=$3::uuid
		RETURNING meta
	`, workspaceID, sessionID, messageID, matchDecision).Scan(&updatedMeta); err != nil {
		return err
	}
	contentHash, err := ArtifactContentHash(ArtifactKindResearchMessage, researchMessageArtifactContent(
		senderType, senderID, targetAgentID, runEventID, body, cardKind, updatedMeta,
	))
	if err != nil {
		return err
	}
	_, err = advanceArtifactVersionTx(ctx, tx, advanceArtifactVersionInput{
		WorkspaceID: workspaceID, SessionID: sessionID, ArtifactID: messageID,
		Kind: ArtifactKindResearchMessage, ContentHash: contentHash, AccessLevel: ArtifactAccessRaw,
	})
	if err != nil {
		return err
	}
	var consumerVersionID string
	if err = tx.QueryRow(ctx, `
		SELECT version.id::text FROM research_artifact_passport passport
		JOIN research_artifact_version version
		  ON (version.workspace_id,version.session_id,version.artifact_id,version.version)=
		     (passport.workspace_id,passport.session_id,passport.id,passport.current_version)
		WHERE passport.workspace_id=$1::uuid AND passport.session_id=$2::uuid AND passport.id=$3::uuid
	`, workspaceID, sessionID, messageID).Scan(&consumerVersionID); err != nil {
		return err
	}
	for _, ref := range refs {
		var inputVersionID string
		if err = tx.QueryRow(ctx, `
			SELECT version.id::text FROM research_artifact_passport passport
			JOIN research_artifact_version version
			  ON (version.workspace_id,version.session_id,version.artifact_id,version.version)=
			     (passport.workspace_id,passport.session_id,passport.id,passport.current_version)
			WHERE passport.workspace_id=$1::uuid AND passport.session_id=$2::uuid
			  AND passport.id=$3::uuid AND passport.entity_kind=$4
		`, workspaceID, sessionID, ref.ArtifactID, string(ref.Kind)).Scan(&inputVersionID); err != nil {
			return fmt.Errorf("%w: unresolved %s reference %s", ErrInvalidContract, ref.Relation, ref.ArtifactID)
		}
		if _, err = tx.Exec(ctx, `
			INSERT INTO research_artifact_input_reference(
			  workspace_id,session_id,consumer_version_id,input_version_id,relation,
			  explicitly_used,purpose,ordinal
			) VALUES($1::uuid,$2::uuid,$3::uuid,$4::uuid,$5,true,'match_decision',$6)
			ON CONFLICT (workspace_id,session_id,consumer_version_id,input_version_id,relation)
			DO NOTHING
		`, workspaceID, sessionID, consumerVersionID, inputVersionID, ref.Relation, ref.Ordinal); err != nil {
			return err
		}
		var persistedOrdinal int
		var persistedPurpose string
		var explicitlyUsed bool
		if err = tx.QueryRow(ctx, `
			SELECT ordinal,purpose,explicitly_used
			FROM research_artifact_input_reference
			WHERE workspace_id=$1::uuid AND session_id=$2::uuid
			  AND consumer_version_id=$3::uuid AND input_version_id=$4::uuid AND relation=$5
		`, workspaceID, sessionID, consumerVersionID, inputVersionID, ref.Relation).Scan(
			&persistedOrdinal, &persistedPurpose, &explicitlyUsed,
		); err != nil {
			return err
		}
		if persistedOrdinal != ref.Ordinal || persistedPurpose != "match_decision" || !explicitlyUsed {
			return fmt.Errorf(
				"%w: conflicting %s lineage for %s at ordinal %d",
				ErrInvalidContract, ref.Relation, ref.ArtifactID, ref.Ordinal,
			)
		}
	}
	return nil
}

func parseResearchMessageMatchDecisionRefs(messageID string, raw json.RawMessage) ([]researchMessageInputRef, error) {
	var payload researchMessageMatchDecisionRefs
	if err := json.Unmarshal(raw, &payload); err != nil {
		return nil, fmt.Errorf("%w: invalid match decision: %v", ErrInvalidContract, err)
	}
	if payload.UtteranceID != messageID {
		return nil, fmt.Errorf("%w: match decision utterance does not own the Message", ErrInvalidContract)
	}
	refs := make([]researchMessageInputRef, 0, 1+len(payload.MatchedNodeIDs)+len(payload.Decisions))
	add := func(id string, kind ArtifactEntityKind, relation string, ordinal int) error {
		if _, err := uuid.Parse(id); err != nil {
			return fmt.Errorf("%w: %s reference is not a UUID", ErrInvalidContract, relation)
		}
		refs = append(refs, researchMessageInputRef{ArtifactID: id, Kind: kind, Relation: relation, Ordinal: ordinal})
		return nil
	}
	if payload.PrimaryAnchorNode != "" {
		if err := add(payload.PrimaryAnchorNode, ArtifactKindGraphNode, "match_primary_anchor", 0); err != nil {
			return nil, err
		}
	}
	for i, id := range payload.MatchedNodeIDs {
		if err := add(id, ArtifactKindGraphNode, "match_candidate", i); err != nil {
			return nil, err
		}
	}
	seenDecision := make(map[string]struct{}, len(payload.Decisions))
	for i, decision := range payload.Decisions {
		if _, duplicate := seenDecision[decision.NodeID]; duplicate {
			return nil, fmt.Errorf("%w: duplicate match decision node", ErrInvalidContract)
		}
		seenDecision[decision.NodeID] = struct{}{}
		if err := add(decision.NodeID, ArtifactKindGraphNode, "match_decision", i); err != nil {
			return nil, err
		}
	}
	return refs, nil
}
