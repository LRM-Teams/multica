package service

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"time"

	"github.com/jackc/pgx/v5/pgtype"

	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

// MixedRLFreezeService turns the provisional persisted ledger into one frozen
// snapshot. Its inputs are database identities only: no caller may choose the
// manifest, hash, terminal ownership, or terminal status.
type MixedRLFreezeService struct {
	queries   *db.Queries
	txStarter TxStarter
}

type MixedRLFreezeResult struct {
	Snapshot FrozenSnapshotRecord
	Run      EnvDispatchRunRecord
}

func NewMixedRLFreezeService(queries *db.Queries, txStarter TxStarter) *MixedRLFreezeService {
	return &MixedRLFreezeService{queries: queries, txStarter: txStarter}
}

func (s *MixedRLFreezeService) Freeze(ctx context.Context, runID pgtype.UUID, timedOut bool) (MixedRLFreezeResult, error) {
	if err := requireMixedRLQueries(s.queries); err != nil {
		return MixedRLFreezeResult{}, err
	}
	if s.txStarter == nil {
		return MixedRLFreezeResult{}, errors.New("transaction starter is required")
	}
	run, err := s.queries.GetMixedRLRun(ctx, runID)
	if err != nil {
		return MixedRLFreezeResult{}, err
	}
	if run.Status == "completed" || run.Status == "failed_timeout" {
		return MixedRLFreezeResult{}, fmt.Errorf("run is already frozen as %q", run.Status)
	}
	if timedOut {
		if run.Status != "running" && run.Status != "quiet_candidate" {
			return MixedRLFreezeResult{}, fmt.Errorf("timeout freeze requires active run, got %q", run.Status)
		}
	} else if run.Status != "quiet_candidate" {
		return MixedRLFreezeResult{}, fmt.Errorf("quiet freeze requires quiet_candidate status, got %q", run.Status)
	}

	ledger := NewProviderCallLedger(s.queries, s.txStarter)
	status := "completed"
	if timedOut {
		status = "failed_timeout"
	}
	snapshot, terminalRun, err := ledger.FreezeAndComplete(ctx, FrozenSnapshotInput{
		// Builder replaces these syntactically-valid placeholders inside the
		// freeze transaction after it fences all new graph mutations.
		SnapshotID: "sha256:pending", SnapshotHash: "sha256:pending", RunID: runID, RunStatus: status,
		SchemaVersion: "1", NormalizationVersion: "1", CanonicalManifest: []byte(`{"calls":[],"associations":[],"terminals":[]}`),
		Build: func(ctx context.Context, qtx *db.Queries, frozen FrozenSnapshotInput) (FrozenSnapshotInput, error) {
			agents, err := qtx.ListMixedRLRunAgents(ctx, runID)
			if err != nil {
				return frozen, err
			}
			calls, err := qtx.ListMixedRLProviderCallsCanonical(ctx, runID)
			if err != nil {
				return frozen, err
			}
			associations, err := qtx.ListMixedRLSegmentCallsCanonical(ctx, runID)
			if err != nil {
				return frozen, err
			}
			owned := make(map[string]bool, len(associations))
			for _, association := range associations {
				if association.AssociationKind == "owned" {
					owned[association.ProviderCallID] = true
				}
			}
			terminalIDs := make([]string, 0, len(agents))
			nextOrdinal, err := qtx.CountMixedRLSegments(ctx, runID)
			if err != nil {
				return frozen, err
			}
			qledger := &ProviderCallLedger{queries: qtx}
			for _, agent := range agents {
				unassigned := make([]db.PiProviderCall, 0)
				for _, call := range calls {
					if call.RunAgentID == agent.RunAgentID && !owned[call.CallID] {
						unassigned = append(unassigned, call)
					}
				}
				if len(unassigned) == 0 {
					continue
				}
				nextOrdinal++
				terminalID := "terminal:" + agent.RunAgentID.String()
				if _, err := qledger.InsertSegment(ctx, SegmentInput{SegmentID: terminalID, RunID: runID, RunAgentID: agent.RunAgentID, Kind: "terminal", SegmentOrdinal: nextOrdinal, ProvisionalAt: time.Now().UTC()}); err != nil {
					return frozen, err
				}
				for _, call := range unassigned {
					if err := qledger.AssociateProviderCall(ctx, SegmentCallAssociationInput{SegmentID: terminalID, ProviderCallID: call.CallID, CallOrdinal: call.CallOrdinal, AssociationKind: "owned"}); err != nil {
						return frozen, err
					}
				}
				terminalIDs = append(terminalIDs, terminalID)
			}
			if err := finalizeMixedRLCausalEdges(ctx, qtx, qledger, runID); err != nil {
				return frozen, err
			}
			allCalls, err := qtx.ListMixedRLProviderCallsCanonical(ctx, runID)
			if err != nil {
				return frozen, err
			}
			segments, err := qtx.ListMixedRLRunSegmentsCanonical(ctx, runID)
			if err != nil {
				return frozen, err
			}
			allAssociations, err := qtx.ListMixedRLSegmentCallsCanonical(ctx, runID)
			if err != nil {
				return frozen, err
			}
			edges, err := qtx.ListMixedRLCausalEdgesCanonical(ctx, runID)
			if err != nil {
				return frozen, err
			}
			auditEvents, err := qtx.ListMixedRLRunAuditEvents(ctx, runID)
			if err != nil {
				return frozen, err
			}
			manifest, snapshotID, err := canonicalMixedRLManifest(allCalls, segments, allAssociations, edges, auditEvents)
			if err != nil {
				return frozen, err
			}
			frozen.CanonicalManifest, frozen.SnapshotID, frozen.SnapshotHash = manifest, snapshotID, snapshotID
			return frozen, nil
		},
	})
	if err != nil {
		return MixedRLFreezeResult{}, err
	}
	return MixedRLFreezeResult{Snapshot: snapshot, Run: terminalRun}, nil
}

// GetFrozenRunDAG returns the immutable run-scoped snapshot. Readiness is the
// mixed run lifecycle alone; callers must not consult root-task status or
// dense-per-session coverage for this path.
func (s *MixedRLFreezeService) GetFrozenRunDAG(ctx context.Context, runID pgtype.UUID, snapshotID string) (FrozenDAGRecord, error) {
	return NewProviderCallLedger(s.queries, s.txStarter).GetFrozenDAG(ctx, runID, snapshotID)
}

// ReapMixedRLQuiescence is the server scheduler entry point. It re-evaluates
// each due candidate against the current persisted counters, then delegates
// publication to Freeze, whose row lock resolves competing scheduler ticks.
func (s *MixedRLFreezeService) ReapMixedRLQuiescence(ctx context.Context, now time.Time) ([]MixedRLFreezeResult, error) {
	if err := requireMixedRLQueries(s.queries); err != nil {
		return nil, err
	}
	candidates, err := s.queries.ListMixedRLQuiescenceCandidates(ctx, timestamptz(now))
	if err != nil {
		return nil, err
	}
	store := NewEnvDispatchRunStore(s.queries)
	results := make([]MixedRLFreezeResult, 0, len(candidates))
	for _, candidate := range candidates {
		decision, err := store.EvaluateQuiescence(ctx, candidate.RunID, now)
		if err != nil {
			// A concurrent activity event or freezer may have changed this row
			// after the scan. The next tick will re-evaluate its persisted state.
			continue
		}
		if !decision.FreezeDue {
			continue
		}
		result, err := s.Freeze(ctx, candidate.RunID, decision.TimedOut)
		if err != nil {
			// Freeze owns the row-level race. A losing scheduler is harmless and
			// must not prevent independent due runs in this same sweep.
			continue
		}
		results = append(results, result)
	}
	return results, nil
}

func canonicalMixedRLManifest(calls []db.PiProviderCall, segments []db.InteractionDagRunSegment, associations []db.InteractionDagSegmentProviderCall, edges []db.InteractionDagCausalEdge, auditEvents []db.EnvDispatchRunAuditEvent) ([]byte, string, error) {
	callIDs := make([]string, 0, len(calls))
	for _, call := range calls {
		callIDs = append(callIDs, call.CallID)
	}
	associationIDs := make([]string, 0, len(associations))
	for _, association := range associations {
		associationIDs = append(associationIDs, association.SegmentID+":"+association.ProviderCallID+":"+association.AssociationKind)
	}
	sort.Strings(associationIDs)
	segmentIDs := make([]string, 0, len(segments))
	for _, segment := range segments {
		segmentIDs = append(segmentIDs, segment.SegmentID)
	}
	edgeIDs := make([]string, 0, len(edges))
	for _, edge := range edges {
		edgeIDs = append(edgeIDs, edge.EdgeID.String())
	}
	captureGaps := make([]string, 0)
	for _, event := range auditEvents {
		if event.Kind == "capture_gap" {
			captureGaps = append(captureGaps, event.RunAgentID.String()+":"+event.TurnID.String()+":"+event.Reason)
		}
	}
	sort.Strings(captureGaps)
	manifest, err := json.Marshal(struct {
		Calls        []string `json:"calls"`
		Segments     []string `json:"segments"`
		Associations []string `json:"associations"`
		Edges        []string `json:"edges"`
		CaptureGaps  []string `json:"capture_gaps"`
	}{Calls: callIDs, Segments: segmentIDs, Associations: associationIDs, Edges: edgeIDs, CaptureGaps: captureGaps})
	if err != nil {
		return nil, "", err
	}
	digest := sha256.Sum256(manifest)
	hash := "sha256:" + hex.EncodeToString(digest[:])
	return manifest, hash, nil
}
