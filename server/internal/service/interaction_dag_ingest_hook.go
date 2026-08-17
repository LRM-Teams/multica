// SPDX-License-Identifier: Apache-2.0

package service

import (
	"context"
	"log/slog"

	"github.com/multica-ai/multica/server/internal/memorygraph"
)

// SegmentIngestHook is the narrow interface the interaction-dag seams use to
// feed closed segments into the graph-memory reviewer's staging area (design
// §5.1: segment close -> staging source summary). Implemented by
// memorygraph.Ingester.
type SegmentIngestHook interface {
	Ingest(ctx context.Context, seg memorygraph.SegmentExport) error
}

// SetSegmentIngestHook wires the graph-memory ingest hook. Nil (the default)
// disables it; the hook is fired asynchronously and best-effort, so it never
// changes seam behavior.
func (s *TaskService) SetSegmentIngestHook(h SegmentIngestHook) {
	s.segmentIngestHook = h
}

// fireSegmentIngest invokes the ingest hook on a detached context in a
// goroutine. Best-effort, matching the seam layer: errors are logged and
// swallowed. Nil hook = no-op.
func (s *TaskService) fireSegmentIngest(ctx context.Context, seg memorygraph.SegmentExport) {
	h := s.segmentIngestHook
	if h == nil {
		return
	}
	detached := context.WithoutCancel(ctx)
	go func() {
		if err := h.Ingest(detached, seg); err != nil {
			slog.Warn("interaction_dag: segment ingest failed",
				"segment_id", seg.SegmentID,
				"agent_run_id", seg.AgentRunID,
				"err", err,
			)
		}
	}()
}
