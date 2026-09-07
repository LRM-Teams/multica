// SPDX-License-Identifier: Apache-2.0

package service

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/multica-ai/multica/server/internal/memorygraph"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

// MemoryRetentionPolicy is the versioned per-workspace retention contract
// (spec §13). All fields are whole days bounded by the platform caps.
// DiagnosticThinkingDays bounds how long diagnostic provider thinking
// survives in the hot store (spec §12.2): short-term incident/debug data
// only, never extendable past the platform ceiling.
type MemoryRetentionPolicy struct {
	Version                int64 `json:"version"`
	TrajectoryHotDays      int   `json:"trajectory_hot_days"`
	ArchiveDays            int   `json:"archive_days"`
	TraceHotDays           int   `json:"trace_hot_days"`
	DiagnosticThinkingDays int   `json:"diagnostic_thinking_days"`
}

// MemoryRetentionUpdate is a CAS policy change: every value must stay
// within the platform caps, and ExpectedVersion must name the version the
// caller saw.
type MemoryRetentionUpdate struct {
	TrajectoryHotDays      int   `json:"trajectory_hot_days"`
	ArchiveDays            int   `json:"archive_days"`
	TraceHotDays           int   `json:"trace_hot_days"`
	DiagnosticThinkingDays int   `json:"diagnostic_thinking_days"`
	ExpectedVersion        int64 `json:"expected_version"`
}

// Validate enforces the platform caps server-side (the DB CHECKs are the
// second wall).
func (u MemoryRetentionUpdate) Validate() error {
	if u.TrajectoryHotDays <= 0 || u.ArchiveDays <= 0 || u.TraceHotDays <= 0 {
		return ErrMemoryRetentionDaysGlobal
	}
	if u.DiagnosticThinkingDays <= 0 || u.DiagnosticThinkingDays > MemoryRetentionThinkingCapDays {
		return fmt.Errorf("%w: diagnostic_thinking_days must be between 1 and %d (hard platform ceiling, spec §12.2)",
			ErrMemoryRetentionCap, MemoryRetentionThinkingCapDays)
	}
	if u.TrajectoryHotDays > MemoryRetentionTrajectoryHotCapDays ||
		u.ArchiveDays > MemoryRetentionArchiveCapDays ||
		u.TraceHotDays > MemoryRetentionTraceHotCapDays {
		return fmt.Errorf("%w: trajectory<=%d archive<=%d trace<=%d",
			ErrMemoryRetentionCap, MemoryRetentionTrajectoryHotCapDays,
			MemoryRetentionArchiveCapDays, MemoryRetentionTraceHotCapDays)
	}
	return nil
}

// MemoryRetentionService owns the policy surface and the idempotent
// retention sweeps (spec §13 + plan Task 17).
type MemoryRetentionService struct {
	pool    *pgxpool.Pool
	archive *MemoryArchiveService
	now     func() time.Time
}

func NewMemoryRetentionService(pool *pgxpool.Pool, archive *MemoryArchiveService) *MemoryRetentionService {
	return &MemoryRetentionService{pool: pool, archive: archive, now: time.Now}
}

// EnsureBootstrapPolicy binds a workspace to the explicit bootstrap
// version 1 (90/365/30/30) when it has no policy yet — the migration does
// this for existing workspaces; this covers new ones.
func (s *MemoryRetentionService) EnsureBootstrapPolicy(ctx context.Context, workspaceID pgtype.UUID) error {
	q := db.New(s.pool)
	_, err := q.EnsureBootstrapMemoryRetentionPolicy(ctx, workspaceID)
	return err
}

// CurrentPolicy returns the workspace's active (highest-version) policy,
// binding the bootstrap version on first touch.
func (s *MemoryRetentionService) CurrentPolicy(ctx context.Context, workspaceID pgtype.UUID) (MemoryRetentionPolicy, error) {
	if s == nil || s.pool == nil {
		return MemoryRetentionPolicy{}, errors.New("memory retention service not configured")
	}
	if err := s.EnsureBootstrapPolicy(ctx, workspaceID); err != nil {
		return MemoryRetentionPolicy{}, fmt.Errorf("retention bootstrap: %w", err)
	}
	row, err := db.New(s.pool).CurrentMemoryRetentionPolicy(ctx, workspaceID)
	if err != nil {
		return MemoryRetentionPolicy{}, fmt.Errorf("retention policy: %w", err)
	}
	return retentionPolicyFromRow(row), nil
}

func retentionPolicyFromRow(row db.CurrentMemoryRetentionPolicyRow) MemoryRetentionPolicy {
	return MemoryRetentionPolicy{
		Version:                row.Version,
		TrajectoryHotDays:      int(row.TrajectoryHotDays),
		ArchiveDays:            int(row.ArchiveDays),
		TraceHotDays:           int(row.TraceHotDays),
		DiagnosticThinkingDays: int(row.DiagnosticThinkingDays),
	}
}

// UpdatePolicy appends the next version after a CAS check. Shortening the
// archive window immediately TIGHTENS every existing manifest's erase
// deadline (LEAST — never extended past its originally bound date); a
// later re-lengthening applies to NEW archives only, honoring the
// "cannot change existing retention commitments without a migration note"
// contract.
func (s *MemoryRetentionService) UpdatePolicy(
	ctx context.Context, workspaceID pgtype.UUID, update MemoryRetentionUpdate, actor string,
) (MemoryRetentionPolicy, error) {
	if s == nil || s.pool == nil {
		return MemoryRetentionPolicy{}, errors.New("memory retention service not configured")
	}
	if err := update.Validate(); err != nil {
		return MemoryRetentionPolicy{}, err
	}
	if actor == "" {
		return MemoryRetentionPolicy{}, errors.New("retention policy update requires an actor")
	}
	q := db.New(s.pool)
	current, err := s.CurrentPolicy(ctx, workspaceID)
	if err != nil {
		return MemoryRetentionPolicy{}, err
	}
	if update.ExpectedVersion != current.Version {
		return MemoryRetentionPolicy{}, fmt.Errorf("%w: expected=%d have=%d",
			ErrMemoryRetentionVersion, update.ExpectedVersion, current.Version)
	}
	row, err := q.InsertMemoryRetentionPolicy(ctx, db.InsertMemoryRetentionPolicyParams{
		WorkspaceID: workspaceID, Version: current.Version + 1,
		TrajectoryHotDays:      int32(update.TrajectoryHotDays),
		ArchiveDays:            int32(update.ArchiveDays),
		TraceHotDays:           int32(update.TraceHotDays),
		DiagnosticThinkingDays: int32(update.DiagnosticThinkingDays),
		UpdatedBy:              actor,
	})
	if err != nil {
		return MemoryRetentionPolicy{}, fmt.Errorf("retention policy write: %w", err)
	}
	if update.ArchiveDays < current.ArchiveDays {
		earliest := s.now().AddDate(0, 0, -update.ArchiveDays)
		if _, err := q.TightenMemoryArchiveEraseDue(ctx, db.TightenMemoryArchiveEraseDueParams{
			WorkspaceID: workspaceID, EraseDue: pgTimestamptz(earliest),
		}); err != nil {
			return MemoryRetentionPolicy{}, fmt.Errorf("retention tighten: %w", err)
		}
	}
	return MemoryRetentionPolicy{
		Version: row.Version, TrajectoryHotDays: int(row.TrajectoryHotDays),
		ArchiveDays: int(row.ArchiveDays), TraceHotDays: int(row.TraceHotDays),
		DiagnosticThinkingDays: int(row.DiagnosticThinkingDays),
	}, nil
}

// ReportDueDiagnosticThinking is the dry-run/report mode of the thinking
// sweep (spec §12.11: report first): it counts expired diagnostic provider
// thinking messages without erasing anything.
func (s *MemoryRetentionService) ReportDueDiagnosticThinking(ctx context.Context, workspaceID pgtype.UUID) (int, error) {
	if s == nil || s.pool == nil {
		return 0, errors.New("memory retention service not configured")
	}
	policy, err := s.CurrentPolicy(ctx, workspaceID)
	if err != nil {
		return 0, err
	}
	cutoff := s.now().AddDate(0, 0, -policy.DiagnosticThinkingDays)
	due, err := db.New(s.pool).ReportDueDiagnosticThinking(ctx, db.ReportDueDiagnosticThinkingParams{
		WorkspaceID: workspaceID, CreatedAt: pgTimestamptz(cutoff),
	})
	if err != nil {
		return 0, fmt.Errorf("thinking report: %w", err)
	}
	return int(due), nil
}

// SweepDue runs the four idempotent retention streams for every
// policy-bound workspace: archive hot blobs past their hot window, sweep
// full query/Explore trace windows past their hot window, erase expired
// diagnostic provider thinking in place, and cryptographically erase due
// archives. Returns the number of actions taken. The sweep cursor records
// the last pass per stream.
func (s *MemoryRetentionService) SweepDue(ctx context.Context, limit int32) (int, error) {
	if s == nil || s.pool == nil {
		return 0, errors.New("memory retention service not configured")
	}
	if limit <= 0 {
		limit = 64
	}
	q := db.New(s.pool)
	workspaceRows, err := q.ListMemoryRetentionWorkspaceIDs(ctx)
	if err != nil {
		return 0, fmt.Errorf("retention workspaces: %w", err)
	}
	actions := 0
	now := s.now()
	for _, workspaceID := range workspaceRows {
		policy, err := q.CurrentMemoryRetentionPolicy(ctx, workspaceID)
		if err != nil {
			return actions, fmt.Errorf("retention policy: %w", err)
		}
		// Trace sweep: delete whole query-log windows whose newest entry is
		// older than the workspace's trace hot window.
		if swept, err := sweepWorkspaceTraceWindows(workspaceID, int(policy.TraceHotDays), now); err == nil {
			actions += swept
		}
		// Thinking sweep: in-place content erase for diagnostic provider
		// thinking past its (<=30d) window (spec §12.2). Rows, types, and seq
		// order stay intact so sanitized trajectories remain contiguous;
		// only content/output/input are cleared, and the erase is
		// idempotent.
		thinkingCutoff := now.AddDate(0, 0, -int(policy.DiagnosticThinkingDays))
		erased, err := q.EraseDueDiagnosticThinking(ctx, db.EraseDueDiagnosticThinkingParams{
			WorkspaceID: workspaceID, CreatedAt: pgTimestamptz(thinkingCutoff),
		})
		if err != nil {
			return actions, fmt.Errorf("thinking sweep: %w", err)
		}
		actions += int(erased)
		if err := q.UpsertMemoryRetentionSweepCursor(ctx, db.UpsertMemoryRetentionSweepCursorParams{
			WorkspaceID: workspaceID, LastTraceSweepAt: pgTimestamptz(now),
			LastThinkingSweepAt: pgTimestamptz(now),
		}); err != nil {
			return actions, fmt.Errorf("retention cursor: %w", err)
		}
	}
	// Archive streams run workspace-independent through the shared queues.
	if s.archive != nil {
		if archived, err := s.archive.ArchiveDue(ctx, limit); err == nil {
			actions += archived
		} else {
			return actions, fmt.Errorf("retention archive: %w", err)
		}
		if erased, err := s.archive.EraseDue(ctx, limit); err == nil {
			actions += erased
		} else {
			return actions, fmt.Errorf("retention erase: %w", err)
		}
	}
	return actions, nil
}

// sweepWorkspaceTraceWindows deletes query-log windows of every graph
// store of one workspace whose newest entry predates the cutoff. Windows
// are full query/Explore traces (spec §13: encrypted hot 30 days — the
// stores live inside the workspace-scoped encrypted-at-rest volume; the
// sweep removes them entirely at expiry).
func sweepWorkspaceTraceWindows(workspaceID pgtype.UUID, hotDays int, now time.Time) (int, error) {
	root, err := graphMemoryWorkspacesRoot()
	if err != nil {
		return 0, err
	}
	ws := workspaceID.String()
	cutoff := now.AddDate(0, 0, -hotDays)
	swept := 0
	for _, kind := range []memorygraph.GraphDirKind{memorygraph.GraphDirKindProject, memorygraph.GraphDirKindChannel} {
		// A nil owner resolves to the kind's parent directory; the
		// per-owner stores are enumerated below it.
		base, err := memorygraph.DirForScope(root, ws, kind, "00000000-0000-0000-0000-000000000000")
		if err != nil {
			continue
		}
		parent := filepath.Dir(base)
		owners, err := listDirIDs(parent)
		if err != nil {
			continue
		}
		for _, owner := range owners {
			store := memorygraph.NewStore(filepath.Join(parent, owner))
			deleted, err := store.SweepQueryLogWindows(cutoff)
			if err != nil {
				continue
			}
			swept += deleted
		}
	}
	return swept, nil
}

// listDirIDs lists the child directory names of dir; a missing dir is
// empty, not an error.
func listDirIDs(dir string) ([]string, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	ids := make([]string, 0, len(entries))
	for _, e := range entries {
		if e.IsDir() {
			ids = append(ids, e.Name())
		}
	}
	return ids, nil
}
