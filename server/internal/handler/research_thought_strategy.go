package handler

import (
	"encoding/json"
	"strings"
	"time"

	"github.com/multica-ai/multica/server/internal/researchrun"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

// Thought-strategy panel states (LRM-1318 / LRM-1306).
const (
	researchThoughtStateDrafting = "drafting"
	researchThoughtStateActive   = "active"
	researchThoughtStateSettled  = "settled"
)

// ResearchThoughtStrategyResp is one side-panel item for「思路/策略」(LRM-1318).
// Projected on session snapshot; never invent rationale/expected_outcome from title/summary.
type ResearchThoughtStrategyResp struct {
	NodeID           string  `json:"node_id"`
	Rationale        string  `json:"rationale"`
	ExpectedOutcome  string  `json:"expected_outcome"`
	StrategyLabel    *string `json:"strategy_label,omitempty"`
	StrategyRevision *string `json:"strategy_revision,omitempty"`
	State            string  `json:"state"`
	UpdatedAt        string  `json:"updated_at,omitempty"`
}

// mapThoughtStrategies projects LRM-1306 panel rows from graph node payloads.
// Inclusion:
//   - both rationale + expected_outcome non-empty → include
//   - OR explicit state=drafting (partial OK) → include as drafting
//
// Missing both faces and not drafting → omit. Never invent from title/summary.
func mapThoughtStrategies(rows []db.ResearchGraphNode) []ResearchThoughtStrategyResp {
	out := make([]ResearchThoughtStrategyResp, 0)
	for _, n := range rows {
		item, ok := thoughtStrategyFromNode(n)
		if !ok {
			continue
		}
		out = append(out, item)
	}
	return out
}

func thoughtStrategyFromNode(n db.ResearchGraphNode) (ResearchThoughtStrategyResp, bool) {
	return thoughtStrategyFromFrozenFields(
		uuidToString(n.ID), json.RawMessage(n.Payload), timestampToString(n.UpdatedAt),
	)
}

func mapFrozenThoughtStrategies(rows []researchrun.FrozenThoughtStrategyNode) []ResearchThoughtStrategyResp {
	out := make([]ResearchThoughtStrategyResp, 0)
	for _, row := range rows {
		item, ok := thoughtStrategyFromFrozenFields(
			row.ID, row.Payload, row.UpdatedAt.UTC().Format(time.RFC3339Nano),
		)
		if ok {
			out = append(out, item)
		}
	}
	return out
}

func thoughtStrategyFromFrozenFields(nodeID string, payload json.RawMessage, updatedAt string) (ResearchThoughtStrategyResp, bool) {
	obj := payloadObject(payload)
	nested := thoughtStrategyObject(obj)

	rationale := firstPayloadString(nested, obj, "rationale", "how_thinking")
	expected := firstPayloadString(nested, obj, "expected_outcome", "expectedOutcome")
	state := normalizeThoughtStrategyState(firstPayloadString(nested, obj, "state", "strategy_state", "strategyState"))

	hasBoth := rationale != "" && expected != ""
	isDrafting := state == researchThoughtStateDrafting
	if !hasBoth && !isDrafting {
		return ResearchThoughtStrategyResp{}, false
	}
	if !hasBoth {
		state = researchThoughtStateDrafting
	} else if state == "" {
		state = researchThoughtStateActive
	}

	label := optionalTrimmedString(nested, "strategy_label", "strategyLabel")
	if label == nil {
		label = optionalTrimmedString(obj, "strategy_label", "strategyLabel")
	}

	rev := optionalTrimmedString(nested, "strategy_revision", "strategyRevision")
	if rev == nil {
		rev = optionalTrimmedString(obj, "strategy_revision", "strategyRevision")
	}
	if rev == nil && (label != nil || hasBoth) && updatedAt != "" {
		rev = &updatedAt
	}

	return ResearchThoughtStrategyResp{
		NodeID:           nodeID,
		Rationale:        rationale,
		ExpectedOutcome:  expected,
		StrategyLabel:    label,
		StrategyRevision: rev,
		State:            state,
		UpdatedAt:        updatedAt,
	}, true
}

func thoughtStrategyObject(obj map[string]any) map[string]any {
	for _, key := range []string{"thought_strategy", "thoughtStrategy", "strategy"} {
		if nested, ok := obj[key].(map[string]any); ok && nested != nil {
			return nested
		}
	}
	return nil
}

func normalizeThoughtStrategyState(raw string) string {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case researchThoughtStateDrafting, "draft", "整理中", "进行中占位":
		return researchThoughtStateDrafting
	case researchThoughtStateActive, "in_progress", "running", "有思路":
		return researchThoughtStateActive
	case researchThoughtStateSettled, "done", "stable", "已落稳":
		return researchThoughtStateSettled
	default:
		return ""
	}
}
