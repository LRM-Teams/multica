package handler

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/jackc/pgx/v5/pgtype"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

var researchStageOrder = []string{"s1_plan", "s2_sources", "s3_validation", "s4_delivery"}

func nextResearchStage(current string) string {
	for i, s := range researchStageOrder {
		if s == current {
			if i+1 < len(researchStageOrder) {
				return researchStageOrder[i+1]
			}
			return "done"
		}
	}
	return "s1_plan"
}

// evaluateResearchStage runs a staged quality gate (not per-step).
// Hard rules encode the product bar; findings feed fleet-scoped feedback.
func (h *Handler) evaluateResearchStage(ctx context.Context, workspaceID pgtype.UUID, session db.ResearchSession) (db.ResearchStageEval, string, error) {
	stage := session.CurrentStage
	nodes, _ := h.Queries.ListResearchGraphNodes(ctx, db.ListResearchGraphNodesParams{SessionID: session.ID, WorkspaceID: workspaceID})
	sources, _ := h.Queries.ListResearchSources(ctx, db.ListResearchSourcesParams{SessionID: session.ID, WorkspaceID: workspaceID})
	report, reportErr := h.Queries.GetLatestResearchReport(ctx, db.GetLatestResearchReportParams{SessionID: session.ID, WorkspaceID: workspaceID})

	var findings []string
	score := 1.0
	passed := true

	countType := func(t string) int {
		n := 0
		for _, node := range nodes {
			if node.NodeType == t {
				n++
			}
		}
		return n
	}

	plan := buildResearchAdaptivePlan(session.Goal)
	withWhy := 0
	for _, s := range sources {
		if sourceWhyFromPayload(s.Payload) != "" {
			withWhy++
		}
	}

	switch stage {
	case "s1_plan":
		if countType("goal") == 0 && countType("subquestion") == 0 {
			passed = false
			score -= 0.5
			findings = append(findings, "missing goal/subquestion decomposition")
		}
		// Adaptive depth: expect a multi-dimension tree, not a thin single Q.
		if countType("subquestion") < 3 {
			passed = false
			score -= 0.2
			findings = append(findings, "need ≥3 dimension/subquestion nodes (adaptive depth tree)")
		}
		families := map[string]bool{}
		for _, node := range nodes {
			if node.NodeType != "subquestion" {
				continue
			}
			var payload map[string]any
			if len(node.Payload) > 0 {
				_ = json.Unmarshal(node.Payload, &payload)
			}
			if fam, ok := payload["dimension_family"].(string); ok && fam != "" {
				families[fam] = true
			}
		}
		if len(families) < 2 && countType("subquestion") >= 3 {
			// Soft signal: prefer dimension_family tagging when present.
			findings = append(findings, "prefer dimension_family on subquestions for adaptive routing")
		}
		if len(findings) == 0 {
			findings = append(findings, fmt.Sprintf("plan gate passed (fine_domain=%s)", plan.FineDomain))
		}
	case "s2_sources":
		if len(sources) < 3 {
			passed = false
			score -= 0.4
			findings = append(findings, "need at least 3 weighted sources")
		}
		classes := map[string]bool{}
		high := 0
		for _, s := range sources {
			classes[s.SourceClass] = true
			if s.CredibilityWeight >= 0.7 {
				high++
			}
		}
		if len(classes) < 2 {
			passed = false
			score -= 0.2
			findings = append(findings, "sources lack class diversity")
		}
		if high == 0 {
			passed = false
			score -= 0.2
			findings = append(findings, "no high-credibility source (>=0.7)")
		}
		// Visible routing rationale: majority of sources should carry why.
		if len(sources) >= 3 && withWhy*2 < len(sources) {
			passed = false
			score -= 0.2
			findings = append(findings, "need payload.why on most sources (why this source / dimension)")
		}
	case "s3_validation":
		if countType("conflict") == 0 && len(sources) >= 2 {
			// Soft fail if multiple sources but no conflict/adjudication note
			hasFinding := countType("finding") > 0
			if !hasFinding {
				passed = false
				score -= 0.3
				findings = append(findings, "need findings and/or explicit conflict adjudication")
			}
		}
		if countType("finding") == 0 {
			passed = false
			score -= 0.3
			findings = append(findings, "no validated findings")
		}
	case "s4_delivery":
		if reportErr != nil || len(report.ContentMd) < 80 {
			passed = false
			score -= 0.5
			findings = append(findings, "report content too thin")
		}
		if len(sources) < 3 {
			passed = false
			score -= 0.2
			findings = append(findings, "delivery requires multi-source evidence")
		}
		if plan.DeliveryLike && reportErr == nil && !reportHasHumanAIBoundary(report.ContentMd) {
			passed = false
			score -= 0.3
			findings = append(findings, "delivery-like goal requires human↔AI boundary in report (AI ceiling / must-have-human / human vs AI)")
		}
	default:
		passed = false
		findings = append(findings, "unknown stage")
	}

	if score < 0 {
		score = 0
	}
	remediation := ""
	if !passed {
		remediation = fmt.Sprintf("Stage %s failed. Fix: %v", stage, findings)
	} else {
		remediation = fmt.Sprintf("Stage %s passed.", stage)
	}

	findingsJSON, _ := json.Marshal(findings)
	eval, err := h.Queries.CreateResearchStageEval(ctx, db.CreateResearchStageEvalParams{
		WorkspaceID: workspaceID,
		SessionID:   session.ID,
		Stage:       stage,
		Passed:      passed,
		Score:       score,
		Findings:    findingsJSON,
		Remediation: remediation,
	})
	if err != nil {
		return db.ResearchStageEval{}, "", err
	}

	fleet, _ := h.Queries.GetResearchFleetByWorkspace(ctx, workspaceID)
	if fleet.ID.Valid {
		_, _ = h.Queries.CreateResearchFleetFeedback(ctx, db.CreateResearchFleetFeedbackParams{
			WorkspaceID: workspaceID,
			FleetID:     fleet.ID,
			SessionID:   session.ID,
			Stage:       pgtype.Text{String: stage, Valid: true},
			Score:       score,
			Notes:       remediation,
			Metadata:    marshalJSONRaw(map[string]any{"research_fleet_only": true, "findings": findings}),
		})
		if passed {
			h.applyResearchFleetPlaybook(ctx, workspaceID, fleet, stage, findings)
		}
	}

	next := ""
	if passed {
		next = nextResearchStage(stage)
	}
	return eval, next, nil
}

func (h *Handler) applyResearchFleetPlaybook(ctx context.Context, workspaceID pgtype.UUID, fleet db.ResearchFleet, stage string, findings []string) {
	domain := "general"
	version := int32(1)
	if latest, err := h.Queries.GetLatestResearchPlaybook(ctx, db.GetLatestResearchPlaybookParams{
		FleetID:     fleet.ID,
		WorkspaceID: workspaceID,
		Domain:      domain,
	}); err == nil {
		version = latest.Version + 1
	}
	content := fmt.Sprintf("# Research playbook (%s)\n\nUpdated after stage %s.\n\nFindings:\n", domain, stage)
	for _, f := range findings {
		content += "- " + f + "\n"
	}
	_, _ = h.Queries.CreateResearchPlaybook(ctx, db.CreateResearchPlaybookParams{
		WorkspaceID: workspaceID,
		FleetID:     fleet.ID,
		Domain:      domain,
		Version:     version,
		ContentMd:   content,
	})
}
