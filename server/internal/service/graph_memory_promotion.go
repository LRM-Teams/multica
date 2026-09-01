// SPDX-License-Identifier: Apache-2.0

package service

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/multica-ai/multica/server/internal/memorygraph"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
	"github.com/multica-ai/multica/server/pkg/redact"
)

// PromotionPolicyVersion stamps every allowed promotion decision and the
// derived node it authorizes. It changes only with a deliberate policy
// revision.
const PromotionPolicyVersion = "project-promotion-v1"

// Durable evidence kinds (spec §11). A promotion must cite at least one
// valid entry; the LLM proposes, the server decides.
const (
	PromotionEvidenceHumanConfirmation = "human_confirmation"
	PromotionEvidenceFormalDecision    = "formal_decision"
	PromotionEvidenceTrustedReadOnly   = "trusted_read_only"
	PromotionEvidenceCompletedOutcome  = "completed_outcome"
	PromotionEvidenceMultiSource       = "multi_source"
)

// promotionInjectionMarkers are the instruction-injection shapes a derived
// body must never carry (spec §11 security gate; the raw-copy rule below
// keeps injected source text out of summaries independently).
var promotionInjectionMarkers = []string{
	"ignore previous instructions",
	"ignore all previous",
	"disregard previous",
	"reveal your instructions",
	"system prompt:",
	"you must obey",
}

// promotionEvidenceKinds is the closed kind set.
var promotionEvidenceKinds = map[string]bool{
	PromotionEvidenceHumanConfirmation: true,
	PromotionEvidenceFormalDecision:    true,
	PromotionEvidenceTrustedReadOnly:   true,
	PromotionEvidenceCompletedOutcome:  true,
	PromotionEvidenceMultiSource:       true,
}

// PromotionEvidence is one durable-evidence citation of an LLM promotion
// proposal.
type PromotionEvidence struct {
	Kind          string
	RefID         string
	PolicyVersion string
}

// PromotionRequest is the server-side input of one promotion proposal: the
// consolidator's derived-summary node plus its evidence citations.
type PromotionRequest struct {
	WorkspaceID  pgtype.UUID
	ProjectID    pgtype.UUID
	ProposedNode *memorygraph.Node
	Evidence     []PromotionEvidence
	ProposedBy   string
}

// PromotionDecision is the policy verdict. The DerivedNode returned on
// Allowed is the server-stamped projection (visibility=project, evidence
// atom refs, policy version) — never the proposal as-is.
type PromotionDecision struct {
	Allowed       bool
	Reason        string
	DerivedNode   *memorygraph.Node
	PolicyVersion string
	Evidence      []PromotionEvidence
}

// GraphMemoryPromotionPolicy validates durable-evidence Project promotions
// (spec §11). Evaluate is read-only: it never publishes; the winning node is
// published exclusively through the Task 14 coordinator
// (GraphMemoryConsolidationPublishService.PublishPromotion), which locks the
// evidence source guards and rechecks retraction in the commit transaction.
type GraphMemoryPromotionPolicy struct {
	pool *pgxpool.Pool
}

func NewGraphMemoryPromotionPolicy(pool *pgxpool.Pool) *GraphMemoryPromotionPolicy {
	return &GraphMemoryPromotionPolicy{pool: pool}
}

func refusedPromotion(reason string) PromotionDecision {
	return PromotionDecision{Allowed: false, Reason: reason, PolicyVersion: PromotionPolicyVersion}
}

// Evaluate applies the full spec §11 matrix. Infrastructural failures return
// an error; every policy rejection is a Decision with Allowed=false and a
// stable reason code.
func (p *GraphMemoryPromotionPolicy) Evaluate(ctx context.Context, req PromotionRequest) (PromotionDecision, error) {
	if p == nil || p.pool == nil {
		return PromotionDecision{}, errors.New("promotion policy requires a pool")
	}
	if !req.WorkspaceID.Valid || !req.ProjectID.Valid {
		return refusedPromotion("scope_unresolved"), nil
	}
	if req.ProposedNode == nil || strings.TrimSpace(req.ProposedNode.NodeID) == "" {
		return refusedPromotion("empty_proposal"), nil
	}
	if strings.TrimSpace(req.ProposedNode.Body) == "" {
		return refusedPromotion("empty_body"), nil
	}
	if len(req.Evidence) == 0 {
		return refusedPromotion("empty_evidence"), nil
	}
	for _, e := range req.Evidence {
		if !promotionEvidenceKinds[e.Kind] {
			return refusedPromotion("evidence_kind_invalid"), nil
		}
		if e.PolicyVersion != PromotionPolicyVersion {
			return refusedPromotion("policy_version_mismatch"), nil
		}
		if e.Kind != PromotionEvidenceMultiSource && strings.TrimSpace(e.RefID) == "" {
			return refusedPromotion("evidence_ref_missing"), nil
		}
	}

	q := db.New(p.pool)
	citedAtomIDs := make([]string, 0, len(req.Evidence))
	validated := make([]PromotionEvidence, 0, len(req.Evidence))
	var atomRows []db.ListGraphMemoryAtomsByIDsRow
	otherKinds := 0

	for _, e := range req.Evidence {
		switch e.Kind {
		case PromotionEvidenceTrustedReadOnly:
			rows, err := q.ListGraphMemoryAtomsByIDs(ctx, db.ListGraphMemoryAtomsByIDsParams{
				WorkspaceID: req.WorkspaceID, AtomIds: []string{e.RefID},
			})
			if err != nil {
				return PromotionDecision{}, fmt.Errorf("promotion atom evidence: %w", err)
			}
			if len(rows) == 0 {
				return refusedPromotion("atom_not_found"), nil
			}
			atom := rows[0]
			if !strings.EqualFold(atom.ToolTrustClass, "trusted_read_only") {
				return refusedPromotion("atom_not_trusted_read_only"), nil
			}
			if !promotionScopeInLineage(ctx, q, req, atom.ChannelID, atom.ProjectID) {
				return refusedPromotion("cross_lineage_source"), nil
			}
			atomRows = append(atomRows, atom)
			citedAtomIDs = append(citedAtomIDs, e.RefID)
			validated = append(validated, e)
			otherKinds++

		case PromotionEvidenceHumanConfirmation:
			msg, err := q.GetChannelMessageForPromotion(ctx, db.GetChannelMessageForPromotionParams{
				ID: parsePromotionRef(e.RefID), WorkspaceID: req.WorkspaceID,
			})
			if err != nil {
				return refusedPromotion("message_not_found"), nil
			}
			if msg.AuthorType != "user" || !msg.AuthorID.Valid {
				return refusedPromotion("message_author_not_human"), nil
			}
			member, err := q.ExistsWorkspaceMember(ctx, db.ExistsWorkspaceMemberParams{
				WorkspaceID: req.WorkspaceID, UserID: msg.AuthorID,
			})
			if err != nil {
				return PromotionDecision{}, fmt.Errorf("promotion author membership: %w", err)
			}
			if !member {
				return refusedPromotion("author_not_workspace_member"), nil
			}
			if !promotionScopeInLineage(ctx, q, req, msg.ChannelID, pgtype.UUID{}) {
				return refusedPromotion("cross_lineage_source"), nil
			}
			validated = append(validated, e)
			otherKinds++

		case PromotionEvidenceFormalDecision:
			status, err := q.GetIssueForPromotion(ctx, db.GetIssueForPromotionParams{
				ID: parsePromotionRef(e.RefID), WorkspaceID: req.WorkspaceID,
			})
			if err != nil {
				return refusedPromotion("issue_not_found"), nil
			}
			if status != "done" {
				return refusedPromotion("issue_not_formal_decision"), nil
			}
			validated = append(validated, e)
			otherKinds++

		case PromotionEvidenceCompletedOutcome:
			// A completed, non-rolled-back outcome is a canonical task with a
			// published segment; a retracted or dead-lettered outcome fails.
			n, err := q.CountPublishedSegmentsForTask(ctx, db.CountPublishedSegmentsForTaskParams{
				WorkspaceID: req.WorkspaceID, AgentRunID: parsePromotionRef(e.RefID),
			})
			if err != nil {
				return PromotionDecision{}, fmt.Errorf("promotion outcome evidence: %w", err)
			}
			if n == 0 {
				return refusedPromotion("outcome_not_completed"), nil
			}
			retracted, err := q.RetractedMemorySources(ctx, db.RetractedMemorySourcesParams{
				WorkspaceID: req.WorkspaceID, SourceKeys: []string{"task_output:" + e.RefID},
			})
			if err != nil {
				return PromotionDecision{}, fmt.Errorf("promotion outcome retraction check: %w", err)
			}
			if len(retracted) > 0 {
				return refusedPromotion("source_retracted"), nil
			}
			validated = append(validated, e)
			otherKinds++

		case PromotionEvidenceMultiSource:
			// Validated after the loop: it needs ≥2 distinct other sources.
		}
	}
	hasMultiSource := false
	for _, e := range req.Evidence {
		if e.Kind == PromotionEvidenceMultiSource {
			hasMultiSource = true
		}
	}
	if hasMultiSource {
		distinct := map[string]bool{}
		for _, e := range validated {
			distinct[e.RefID] = true
		}
		if len(distinct) < 2 {
			return refusedPromotion("multi_source_insufficient"), nil
		}
		validated = append(validated, PromotionEvidence{
			Kind: PromotionEvidenceMultiSource, PolicyVersion: PromotionPolicyVersion,
		})
	}
	if len(validated) == 0 {
		return refusedPromotion("empty_evidence"), nil
	}

	// Security gates on the derived body: secrets, verbatim source copies,
	// and injection markers all refuse the promotion.
	body := req.ProposedNode.Body
	if redact.Text(body) != body {
		return refusedPromotion("secret_content"), nil
	}
	lower := strings.ToLower(body)
	for _, marker := range promotionInjectionMarkers {
		if strings.Contains(lower, marker) {
			return refusedPromotion("prompt_injection"), nil
		}
	}
	for _, atom := range atomRows {
		private := strings.TrimSpace(atom.Body)
		if len(private) >= 24 && strings.Contains(body, private) {
			return refusedPromotion("derived_body_copies_source"), nil
		}
	}

	// Server-stamped derived node: a new project-visible summary citing the
	// validated evidence atoms — never the proposal's own scope fields.
	derived := *req.ProposedNode
	derived.Visibility = "project"
	derived.ChannelID = ""
	derived.PolicyVersion = PromotionPolicyVersion
	derived.CreatedBy = memorygraph.CreatorPromoter
	if len(citedAtomIDs) > 0 {
		derived.AtomRefs = citedAtomIDs
	}
	derived.Tags = append(derived.Tags, "promoted")
	return PromotionDecision{
		Allowed: true, Reason: "durable_evidence",
		DerivedNode: &derived, PolicyVersion: PromotionPolicyVersion,
		Evidence: validated,
	}, nil
}

func parsePromotionRef(ref string) pgtype.UUID {
	parsed, err := uuid.Parse(ref)
	if err != nil {
		return pgtype.UUID{}
	}
	return pgtype.UUID{Bytes: parsed, Valid: true}
}

// promotionScopeInLineage checks that one evidence scope belongs to the
// target Project lineage: a channel must be currently bound to the target
// project; a project atom must carry the target project id.
func promotionScopeInLineage(ctx context.Context, q *db.Queries, req PromotionRequest, channelID, projectID pgtype.UUID) bool {
	if channelID.Valid {
		bound, err := q.GetGraphMemoryChannelBinding(ctx, db.GetGraphMemoryChannelBindingParams{
			ID: channelID, WorkspaceID: req.WorkspaceID,
		})
		if err != nil || !bound.Valid {
			return false
		}
		return bound.Bytes == req.ProjectID.Bytes
	}
	return projectID.Valid && projectID.Bytes == req.ProjectID.Bytes
}

// ---------------------------------------------------------------------------
// Corrections (spec §11): owner/admin retract immediately; everyone else
// submits a candidate. Both record policy/reason/provenance audit rows.
// ---------------------------------------------------------------------------

// Quarantine consumer kinds. "graph_node" hides a published node from every
// reader; "graph_node_correction" is the inert candidate record a member or
// agent submits for owner/admin review (no reader excludes it — nothing
// references that kind).
const (
	GraphNodeConsumerKind           = "graph_node"
	GraphNodeCorrectionConsumerKind = "graph_node_correction"
)

// GraphMemoryCorrectionService is the handler-facing corrections surface:
// owner/admin retracts are immediate; member/agent submissions become
// reviewable candidates.
type GraphMemoryCorrectionService struct {
	pool *pgxpool.Pool
}

func NewGraphMemoryCorrectionService(pool *pgxpool.Pool) *GraphMemoryCorrectionService {
	return &GraphMemoryCorrectionService{pool: pool}
}

// Retract hides one published graph node immediately (owner/admin path): a
// quarantine row, an attributable registry event, and a deletion-audit row
// commit together.
func (s *GraphMemoryCorrectionService) Retract(ctx context.Context, workspaceID pgtype.UUID, nodeID, actor, reason string) error {
	if s == nil || s.pool == nil {
		return errors.New("graph node correction service not configured")
	}
	return writeGraphNodeCorrection(ctx, s.pool, workspaceID, nodeID, actor, reason, GraphNodeConsumerKind)
}

// Submit records a correction candidate (member/agent path): the node stays
// visible until an owner/admin converts the candidate into a retraction.
func (s *GraphMemoryCorrectionService) Submit(ctx context.Context, workspaceID pgtype.UUID, nodeID, actor, reason string) error {
	if s == nil || s.pool == nil {
		return errors.New("graph node correction service not configured")
	}
	return writeGraphNodeCorrection(ctx, s.pool, workspaceID, nodeID, actor, reason, GraphNodeCorrectionConsumerKind)
}

func writeGraphNodeCorrection(ctx context.Context, pool *pgxpool.Pool, workspaceID pgtype.UUID, nodeID, actor, reason, consumerKind string) error {
	if pool == nil {
		return errors.New("graph node correction requires a pool")
	}
	if strings.TrimSpace(nodeID) == "" {
		return errors.New("graph node correction requires a node id")
	}
	tx, err := pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	retraction := pgtype.UUID{Bytes: uuid.New(), Valid: true}
	if _, err := tx.Exec(ctx, `
		INSERT INTO retraction_registry (id, workspace_id, actor, reason, source_count)
		VALUES ($1, $2, $3, $4, 1)`, retraction, workspaceID, actor, reason); err != nil {
		return fmt.Errorf("graph node correction registry: %w", err)
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO quarantined_pending_recompute (workspace_id, retraction_id, consumer_kind, consumer_id)
		VALUES ($1, $2, $3, $4)`, workspaceID, retraction, consumerKind, nodeID); err != nil {
		return fmt.Errorf("graph node correction quarantine: %w", err)
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO memory_deletion_audit (workspace_id, retraction_id, source_kind, source_id, quarantined_count)
		VALUES ($1, $2, $3, $4, 1)`, workspaceID, retraction, consumerKind, nodeID); err != nil {
		return fmt.Errorf("graph node correction audit: %w", err)
	}
	return tx.Commit(ctx)
}
