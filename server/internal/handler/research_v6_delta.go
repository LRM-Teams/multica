package handler

import (
	"encoding/json"
	"fmt"
	"sort"
)

const researchV6MaximumDeltaEvents = 1000

type researchV6ProjectionEvent struct {
	Sequence int64
	Type     string
	Payload  json.RawMessage
}

func buildResearchV6EventDelta(snapshot researchV6Snapshot, from int64, events []researchV6ProjectionEvent) (researchV6Delta, bool) {
	delta := researchV6Delta{FromSequenceExclusive: from, NodeUpserts: []researchV6ProjectionNode{}, EdgeUpserts: []researchV6ProjectionEdge{}, NodeTombstones: []string{}, EdgeTombstones: []string{}, AffectedRootNodeIDs: []string{}}
	if len(events) == 0 {
		delta.ThroughSequence = from
		return delta, from == snapshot.ThroughEventSequence
	}
	if len(events) > researchV6MaximumDeltaEvents || events[0].Sequence != from+1 || events[len(events)-1].Sequence != snapshot.ThroughEventSequence {
		return delta, false
	}
	for index := 1; index < len(events); index++ {
		if events[index].Sequence != events[index-1].Sequence+1 {
			return delta, false
		}
	}
	nodesByEntity := map[string]researchV6ProjectionNode{}
	for _, node := range snapshot.Nodes {
		nodesByEntity[node.EntityKind+"\x00"+node.EntityID] = node
	}
	affectedNodes := map[string]researchV6ProjectionNode{}
	transition := ""
	for _, event := range events {
		var payload map[string]any
		if err := json.Unmarshal(event.Payload, &payload); err != nil {
			return delta, false
		}
		entityRefs, eventTransition, safe := researchV6EventImpact(event.Type, payload)
		if !safe {
			return delta, false
		}
		if eventTransition != "" {
			transition = eventTransition
		}
		for _, ref := range entityRefs {
			if ref.kind == "__root__" {
				for _, node := range snapshot.Nodes {
					if node.EntityKind == "root" || node.NodeSubtype == "goal" {
						affectedNodes[node.ID] = node
					}
				}
				continue
			}
			if node, exists := nodesByEntity[ref.kind+"\x00"+ref.id]; exists {
				affectedNodes[node.ID] = node
				continue
			}
			// A referenced entity that is absent from the current snapshot may have
			// been deleted; the event lacks a stable tombstone payload.
			return delta, false
		}
	}
	for _, node := range affectedNodes {
		delta.NodeUpserts = append(delta.NodeUpserts, node)
	}
	sort.Slice(delta.NodeUpserts, func(i, j int) bool { return delta.NodeUpserts[i].ID < delta.NodeUpserts[j].ID })
	affectedSet := map[string]struct{}{}
	for _, node := range delta.NodeUpserts {
		affectedSet[node.ID] = struct{}{}
	}
	for _, edge := range snapshot.Edges {
		if _, fromOK := affectedSet[edge.FromNodeID]; fromOK {
			delta.EdgeUpserts = append(delta.EdgeUpserts, edge)
			continue
		}
		if _, toOK := affectedSet[edge.ToNodeID]; toOK {
			delta.EdgeUpserts = append(delta.EdgeUpserts, edge)
		}
	}
	sort.Slice(delta.EdgeUpserts, func(i, j int) bool { return delta.EdgeUpserts[i].ID < delta.EdgeUpserts[j].ID })
	delta.ThroughSequence = snapshot.ThroughEventSequence
	delta.AffectedRootNodeIDs = researchV6RootIDs(snapshot.Nodes)
	if transition != "" {
		delta.TransitionKind = &transition
	}
	return delta, true
}

type researchV6EntityRef struct{ kind, id string }

func researchV6EventImpact(eventType string, payload map[string]any) ([]researchV6EntityRef, string, bool) {
	refs := []researchV6EntityRef{}
	add := func(kind, key string) bool {
		id, ok := payload[key].(string)
		if !ok || id == "" {
			return false
		}
		refs = append(refs, researchV6EntityRef{kind, id})
		return true
	}
	switch eventType {
	case "task_dispatching", "task_dispatched", "task_started", "task_attempt_cancelling", "task_attempt_failed":
		if !add("task", "task_id") || !add("attempt", "attempt_id") {
			return nil, "", false
		}
		transition := ""
		if eventType == "task_dispatched" || eventType == "task_dispatching" {
			transition = "task_dispatched"
		}
		return refs, transition, true
	case "task_result_accepted":
		for _, countKey := range []string{"questions_created", "tasks_created", "sources_created", "observations_created", "claims_created"} {
			if value, ok := payload[countKey].(float64); ok && value > 0 {
				return nil, "", false
			}
		}
		if reportID, _ := payload["report_id"].(string); reportID != "" {
			return nil, "", false
		}
		if !add("task", "task_id") || !add("attempt", "attempt_id") {
			return nil, "", false
		}
		return refs, "result_accepted", true
	case "task_blocked", "control_task_created":
		if !add("task", "task_id") {
			return nil, "", false
		}
		return refs, "", true
	case "run_started", "run_awaiting_confirmation", "run_completed", "run_resumed", "run_paused", "run_cancelled", "run_archived", "budget_exhausted", "execution_circuit_transition", "target_repair_decided", "task_waiting_for_execution_target":
		// These events change the Run summary/activity projection. The compatible
		// root is discoverable from the current snapshot rather than the payload.
		return []researchV6EntityRef{{kind: "__root__"}}, "", true
	default:
		return nil, "", false
	}
}

func validateResearchV6ProjectionEvents(events []researchV6ProjectionEvent) error {
	if len(events) > researchV6MaximumDeltaEvents {
		return fmt.Errorf("projection event page exceeds %d", researchV6MaximumDeltaEvents)
	}
	return nil
}
