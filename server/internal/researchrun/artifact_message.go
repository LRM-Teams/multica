package researchrun

import (
	"context"
	"encoding/json"
	"time"

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
	var senderType, senderID, targetAgentID, body, cardKind string
	var meta []byte
	var createdAt time.Time
	err := tx.QueryRow(ctx, `
		SELECT sender_type, COALESCE(sender_id::text, ''), COALESCE(target_agent_id::text, ''),
		       body, card_kind, meta, created_at
		FROM research_message
		WHERE workspace_id = $1::uuid AND session_id = $2::uuid AND id = $3::uuid
	`, workspaceID, sessionID, messageID).Scan(
		&senderType, &senderID, &targetAgentID, &body, &cardKind, &meta, &createdAt,
	)
	if err != nil {
		return err
	}
	contentHash, err := ArtifactContentHash(ArtifactKindResearchMessage, researchMessageArtifactContent(
		senderType, senderID, targetAgentID, body, cardKind, meta,
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

func researchMessageArtifactContent(senderType, senderID, targetAgentID, body, cardKind string, meta []byte) map[string]any {
	return map[string]any{
		"sender_type":     senderType,
		"sender_id":       senderID,
		"target_agent_id": targetAgentID,
		"body":            body,
		"card_kind":       cardKind,
		"meta":            json.RawMessage(meta),
	}
}
