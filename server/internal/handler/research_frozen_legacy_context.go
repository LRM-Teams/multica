package handler

import (
	"encoding/json"
	"strings"
	"time"

	"github.com/multica-ai/multica/server/internal/researchrun"
)

func mapFrozenLegacySources(rows []researchrun.FrozenLegacySource) []ResearchSourceResp {
	out := make([]ResearchSourceResp, 0, len(rows))
	for _, row := range rows {
		out = append(out, ResearchSourceResp{
			ID: row.ID, SessionID: row.SessionID, URL: row.URL, Title: row.Title,
			SourceClass: row.SourceClass, CredibilityWeight: row.CredibilityWeight,
			Stance: row.Stance, Relevance: row.Relevance, Summary: row.Summary,
			Excerpt: row.Excerpt, Payload: row.Payload,
			CreatedAt: row.CreatedAt.UTC().Format(time.RFC3339Nano),
			UpdatedAt: row.UpdatedAt.UTC().Format(time.RFC3339Nano),
		})
	}
	return out
}

func mapFrozenResearchMessages(rows []researchrun.FrozenResearchMessage) []ResearchMessageResp {
	out := make([]ResearchMessageResp, 0, len(rows))
	for _, row := range rows {
		cardKind := row.CardKind
		if cardKind == "" {
			cardKind = "chat"
		}
		meta := row.Meta
		if len(meta) == 0 {
			meta = json.RawMessage(`{}`)
		}
		out = append(out, ResearchMessageResp{
			ID: row.ID, SessionID: row.SessionID, SenderType: row.SenderType,
			SenderID: optionalFrozenString(row.SenderID), TargetAgentID: optionalFrozenString(row.TargetAgentID),
			Body: row.Body, CardKind: cardKind, Meta: meta,
			MatchDecision: extractMatchDecisionFromMeta(meta, row.ID),
			CreatedAt:     row.CreatedAt.UTC().Format(time.RFC3339Nano),
		})
	}
	return out
}

func mapFrozenProductRounds(rows []researchrun.FrozenProductRound) []ResearchProductRoundCardResp {
	out := make([]ResearchProductRoundCardResp, 0, len(rows))
	for _, row := range rows {
		gaps := row.CoverageGaps
		if len(gaps) == 0 {
			gaps = json.RawMessage(`[]`)
		}
		out = append(out, ResearchProductRoundCardResp{
			ID: row.ID, SessionID: row.SessionID, RoundNumber: row.RoundNumber,
			Decision: row.Decision, CoverageGaps: gaps, ConfidenceNote: row.ConfidenceNote,
			BudgetUsed: row.BudgetUsed, BudgetRemaining: row.BudgetRemaining,
			GoalPatchProposal: optionalFrozenString(row.GoalPatchProposal),
			NextRoundFocus:    optionalFrozenString(row.NextRoundFocus),
			DecidedByAgentID:  optionalFrozenString(row.DecidedByAgentID),
			CreatedAt:         row.CreatedAt.UTC().Format(time.RFC3339Nano),
		})
	}
	return out
}

func mapFrozenResearchReport(row *researchrun.FrozenResearchReport) *ResearchReportResp {
	if row == nil {
		return nil
	}
	return &ResearchReportResp{
		ID: row.ID, SessionID: row.SessionID, Revision: row.Revision,
		ContentMD: row.ContentMD, Structured: row.Structured,
		CreatedAt: row.CreatedAt.UTC().Format(time.RFC3339Nano),
		UpdatedAt: row.UpdatedAt.UTC().Format(time.RFC3339Nano),
	}
}

func optionalFrozenString(value string) *string {
	value = strings.TrimSpace(value)
	if value == "" {
		return nil
	}
	return &value
}
