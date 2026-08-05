package handler

import (
	"encoding/json"
	"strings"

	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

// Tree-edge contract (LRM-1278): only leads_to forms the single-parent display tree.
// Direction: from_node_id = parent, to_node_id = child (matches createResearchGraphNodePublished).
// supports / contradicts / supersedes / abandons are semantic edges and must not
// contribute to parent_id / child_count / descendant_count.
const researchTreeEdgeType = "leads_to"

// Assessment enum (LRM-1278 / product freeze 1268+1286). Missing or illegal → pending_review.
const (
	researchAssessmentTrusted       = "trusted"
	researchAssessmentPendingReview = "pending_review"
	researchAssessmentDetour        = "detour"
)

func mapGraphNodes(rows []db.ResearchGraphNode, edges []db.ResearchGraphEdge) []ResearchGraphNodeResp {
	parentOf, childrenOf := buildResearchTreeIndex(edges)

	out := make([]ResearchGraphNodeResp, 0, len(rows))
	for _, n := range rows {
		id := uuidToString(n.ID)
		payload := json.RawMessage(n.Payload)
		assessment, reason, evidence := assessmentFieldsFromPayload(payload)

		var parentID *string
		if p, ok := parentOf[id]; ok && p != "" {
			parentID = &p
		}
		childIDs := childrenOf[id]
		if childIDs == nil {
			childIDs = []string{}
		}

		out = append(out, ResearchGraphNodeResp{
			ID:               id,
			SessionID:        uuidToString(n.SessionID),
			NodeType:         n.NodeType,
			Title:            n.Title,
			Summary:          n.Summary,
			Status:           n.Status,
			ActorAgentID:     uuidToPtr(n.ActorAgentID),
			Payload:          payload,
			Confidence:       confidenceFromPayload(payload),
			ParentID:         parentID,
			ChildIDs:         childIDs,
			ChildCount:       len(childIDs),
			DescendantCount:  countResearchDescendants(id, childrenOf, nil),
			ThemeKey:         themeKeyFromNode(n.NodeType, payload),
			Phase:            phaseFromPayload(payload),
			Assessment:       assessment,
			Reason:           reason,
			EvidenceSummary:  evidence,
			Content:          contentFacesFromPayload(payload),
			AbandonReason:    abandonReasonFromPayload(payload),
			CreatedAt:        timestampToString(n.CreatedAt),
			UpdatedAt:        timestampToString(n.UpdatedAt),
		})
	}
	return out
}

func mapGraphNodeWithEdge(node db.ResearchGraphNode, edge *db.ResearchGraphEdge) ResearchGraphNodeResp {
	var edges []db.ResearchGraphEdge
	if edge != nil {
		edges = []db.ResearchGraphEdge{*edge}
	}
	return mapGraphNodes([]db.ResearchGraphNode{node}, edges)[0]
}

func buildResearchTreeIndex(edges []db.ResearchGraphEdge) (parentOf map[string]string, childrenOf map[string][]string) {
	parentOf = map[string]string{}
	childrenOf = map[string][]string{}
	for _, e := range edges {
		if e.EdgeType != researchTreeEdgeType {
			continue
		}
		from := uuidToString(e.FromNodeID)
		to := uuidToString(e.ToNodeID)
		if from == "" || to == "" || from == to {
			continue
		}
		// First leads_to into a node wins (edges are listed created_at ASC).
		// Losing parents must not receive the child in childrenOf / counts.
		if _, exists := parentOf[to]; exists {
			continue
		}
		parentOf[to] = from
		childrenOf[from] = append(childrenOf[from], to)
	}
	return parentOf, childrenOf
}

func countResearchDescendants(id string, childrenOf map[string][]string, seen map[string]bool) int {
	if seen == nil {
		seen = map[string]bool{}
	}
	if seen[id] {
		return 0
	}
	seen[id] = true
	total := 0
	for _, child := range childrenOf[id] {
		total++
		total += countResearchDescendants(child, childrenOf, seen)
	}
	return total
}

func themeKeyFromNode(nodeType string, payload json.RawMessage) string {
	obj := payloadObject(payload)
	for _, key := range []string{"theme_key", "themeKey", "dimension_family", "dimensionFamily", "family"} {
		if v, ok := obj[key].(string); ok {
			if t := strings.TrimSpace(v); t != "" {
				return t
			}
		}
	}
	nt := strings.TrimSpace(nodeType)
	if nt == "" {
		return "type:unknown"
	}
	return "type:" + nt
}

func phaseFromPayload(payload json.RawMessage) string {
	obj := payloadObject(payload)
	if v, ok := obj["phase"].(string); ok {
		return strings.TrimSpace(v)
	}
	return ""
}

func assessmentFieldsFromPayload(payload json.RawMessage) (assessment string, reason *string, evidence *string) {
	obj := payloadObject(payload)
	assessment = normalizeResearchAssessment(obj["assessment"])
	reason = optionalTrimmedString(obj, "reason", "assessment_reason")
	evidence = optionalTrimmedString(obj, "evidence_summary", "evidence")
	return assessment, reason, evidence
}

// contentFacesFromPayload projects LRM-1317 four faces. Empty strings are
// intentional neutrals — callers must not invent copy from title/summary.
func contentFacesFromPayload(payload json.RawMessage) ResearchNodeContentFaces {
	obj := payloadObject(payload)
	nested, _ := obj["content"].(map[string]any)
	return ResearchNodeContentFaces{
		Goal:              firstPayloadString(nested, obj, "goal", "goal_text", "node_goal"),
		OperationApproach: firstPayloadString(nested, obj, "operation_approach", "ops_approach"),
		ResearchApproach:  firstPayloadString(nested, obj, "research_approach"),
		Result:            firstPayloadString(nested, obj, "result", "research_result", "result_summary"),
	}
}

func abandonReasonFromPayload(payload json.RawMessage) *string {
	return optionalTrimmedString(payloadObject(payload), "abandon_reason", "deprecate_reason")
}

// firstPayloadString prefers nested content keys, then flat payload aliases.
func firstPayloadString(nested, flat map[string]any, keys ...string) string {
	for _, key := range keys {
		if nested != nil {
			if v, ok := nested[key].(string); ok {
				if t := strings.TrimSpace(v); t != "" {
					return t
				}
			}
		}
		if flat != nil {
			if v, ok := flat[key].(string); ok {
				if t := strings.TrimSpace(v); t != "" {
					return t
				}
			}
		}
	}
	return ""
}

func normalizeResearchAssessment(raw any) string {
	s, _ := raw.(string)
	switch strings.ToLower(strings.TrimSpace(s)) {
	case researchAssessmentTrusted, "high_trust", "valid", "高可信", "有效":
		return researchAssessmentTrusted
	case researchAssessmentDetour, "inaccurate", "wrong_path", "弯路", "不准确":
		return researchAssessmentDetour
	case researchAssessmentPendingReview, "pending", "neutral", "待核", "中性", "":
		return researchAssessmentPendingReview
	default:
		return researchAssessmentPendingReview
	}
}

func optionalTrimmedString(obj map[string]any, keys ...string) *string {
	for _, key := range keys {
		if v, ok := obj[key].(string); ok {
			if t := strings.TrimSpace(v); t != "" {
				return &t
			}
		}
	}
	return nil
}

func payloadObject(payload json.RawMessage) map[string]any {
	if len(payload) == 0 {
		return map[string]any{}
	}
	var obj map[string]any
	if err := json.Unmarshal(payload, &obj); err != nil || obj == nil {
		return map[string]any{}
	}
	return obj
}
