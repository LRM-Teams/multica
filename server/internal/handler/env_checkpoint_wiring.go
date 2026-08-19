// SPDX-License-Identifier: Apache-2.0

package handler

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/multica-ai/multica/server/internal/service"
	"github.com/multica-ai/multica/server/internal/util"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

// newEnvCheckpointService builds the production checkpoint service from the
// handler's queries, sandbox lifecycle, and hubs. It returns nil when the
// handler has no queries (test fixtures build *Handler directly), which keeps
// the injected fake as the test path and leaves the endpoints answering
// "unconfigured" rather than panicking.
//
// Construction alone does not expose anything: every checkpoint endpoint is
// still gated by ENV_CHECKPOINTS_ENABLED. What the flag now gates is a service
// that is actually reachable -- before this, the endpoints were dead even with
// the flag on, because nothing ever set the field.
// newBranchSavepointProvider builds the seam branch dispatch captures its source
// through, and that the mention path looks templates up from. It returns nil for
// a handler with no queries, exactly like the checkpoint service: branch dispatch
// then refuses rather than falling back to a clone that no longer exists.
func newBranchSavepointProvider(h *Handler) service.BranchSavepointResolver {
	if h == nil || h.Queries == nil {
		return nil
	}
	checkpoints := newEnvCheckpointService(h)
	if checkpoints == nil {
		return nil
	}
	lifecycle := newEnvSandboxLifecycleService(h)
	if lifecycle == nil {
		return nil
	}
	return service.NewBranchSavepointProvider(
		checkpoints,
		service.NewSavepointReader(h.Queries),
		service.NewEnvCheckpointLaneRepository(h.Queries),
		lifecycle,
		h.Queries,
	)
}

// savepointReleaserAdapter releases a savepoint through the same delete_template
// path a user-initiated snapshot deletion takes. A savepoint is an ordinary
// sandbox_snapshot with an owning checkpoint, so there is one reclamation path,
// not two.
// The two dependencies are named narrowly so the release decisions -- already
// gone, already queued, still being created, never reached Cube -- are testable
// without a Handler or a database.
type savepointReleaseQueries interface {
	GetSandboxSnapshotForWorkspace(ctx context.Context, arg db.GetSandboxSnapshotForWorkspaceParams) (db.SandboxSnapshot, error)
	DeleteSandboxSnapshot(ctx context.Context, arg db.DeleteSandboxSnapshotParams) error
}

type snapshotTemplateDeletionScheduler interface {
	scheduleSnapshotTemplateDeletion(ctx context.Context, snap db.SandboxSnapshot, wsUUID pgtype.UUID, actorUserID string) (db.SandboxSnapshot, error)
}

type savepointReleaserAdapter struct {
	q         savepointReleaseQueries
	scheduler snapshotTemplateDeletionScheduler
}

// The production types must keep satisfying the narrow surfaces, or the fakes in
// the tests drift from what really runs.
var (
	_ savepointReleaseQueries           = (*db.Queries)(nil)
	_ snapshotTemplateDeletionScheduler = (*Handler)(nil)
)

func (a *savepointReleaserAdapter) ReleaseSavepoint(ctx context.Context, snapshotID, workspaceID, actorUserID string) error {
	snapUUID, err := util.ParseUUID(snapshotID)
	if err != nil {
		return fmt.Errorf("parse snapshot_id: %w", err)
	}
	wsUUID, err := util.ParseUUID(workspaceID)
	if err != nil {
		return fmt.Errorf("parse workspace_id: %w", err)
	}
	snap, err := a.q.GetSandboxSnapshotForWorkspace(ctx, db.GetSandboxSnapshotForWorkspaceParams{
		ID:          snapUUID,
		WorkspaceID: wsUUID,
	})
	if err != nil {
		// Already gone. Release is called while deleting a checkpoint, which
		// must be able to complete; failing here would pin the checkpoint row
		// on a savepoint that no longer exists.
		if errors.Is(err, pgx.ErrNoRows) {
			return nil
		}
		return fmt.Errorf("load savepoint %s: %w", snapshotID, err)
	}
	switch snap.Status {
	case "deleting":
		// A delete_template job is already queued for it.
		return nil
	case "creating":
		// The template is still being written. Deleting now would race the
		// create, so the checkpoint delete is refused and can be retried.
		return fmt.Errorf("savepoint %s is still being created", snapshotID)
	}
	if strings.TrimSpace(snap.CubeSnapshotID) == "" {
		// Never reached Cube, so there is no template to release; drop the row.
		return a.q.DeleteSandboxSnapshot(ctx, db.DeleteSandboxSnapshotParams{
			ID:          snapUUID,
			WorkspaceID: wsUUID,
		})
	}
	_, err = a.scheduler.scheduleSnapshotTemplateDeletion(ctx, snap, wsUUID, actorUserID)
	return err
}

func newEnvCheckpointService(h *Handler) EnvCheckpointServiceAPI {
	if h == nil || h.Queries == nil {
		return nil
	}
	lifecycle := newEnvSandboxLifecycleService(h)
	if lifecycle == nil {
		return nil
	}
	jobs := &envSandboxLifecycleDepsAdapter{h: h}
	dispatch := &envDispatchDepsAdapter{h: h}

	// A nil *daemonws.Hub stored in the interface would be non-nil as an
	// interface value, so the wake fast-path would call through a nil receiver.
	// Pass nothing instead: the daemon's poll loop still re-claims the task,
	// just less promptly.
	var waker service.TaskWakeupNotifier
	if h.DaemonHub != nil {
		waker = h.DaemonHub
	}

	return service.NewEnvCheckpointService(
		service.NewEnvCheckpointRepository(h.Queries),
		service.NewSandboxInstanceSaver(lifecycle),
		service.NewSandboxInstanceResumer(lifecycle),
		service.NewProjectSnapshotReader(h.Queries),
		service.NewInFlightTaskResolver(h.Queries),
		service.ContinuationRegistry{
			SameRuntime: service.NewSameRuntimeContinuation(h.Queries, waker),
			Forked:      service.NewForkedRuntimeContinuation(dispatch),
		},
	).
		WithSavepointCreator(service.NewSavepointCreator(h.Queries, jobs)).
		WithLanes(
			service.NewEnvCheckpointLaneRepository(h.Queries),
			service.NewLaneMaterializer(&laneMaterializerDepsAdapter{
				lifecycle: lifecycle,
				dispatch:  dispatch,
			}),
			service.NewSavepointReader(h.Queries),
		).
		WithSavepointReleaser(&savepointReleaserAdapter{q: h.Queries, scheduler: h})
}

// laneMaterializerDepsAdapter wires lane materialization to the same primitives
// env-dispatch uses, so a lane's sandbox, env, and project copy are produced the
// same way a rollout's are.
type laneMaterializerDepsAdapter struct {
	lifecycle *service.EnvSandboxLifecycleService
	dispatch  *envDispatchDepsAdapter
}

func (a *laneMaterializerDepsAdapter) CreateSandboxInstance(ctx context.Context, in service.CreateSandboxInstanceInput, actorUserID string) (service.SandboxInstanceRef, error) {
	return a.lifecycle.CreateSandboxInstance(ctx, in, actorUserID)
}

func (a *laneMaterializerDepsAdapter) CreateEnv(ctx context.Context, workspaceID string, sandboxIDs []string, parentEnvID string, mode service.EnvMode, domain service.EnvDomain) (string, error) {
	return a.dispatch.CreateEnv(ctx, workspaceID, sandboxIDs, parentEnvID, mode, domain)
}

func (a *laneMaterializerDepsAdapter) CopyProjectSubtree(ctx context.Context, sourceProjectID, workspaceID, envID string) (string, map[string]string, map[string]string, error) {
	return a.dispatch.CopyProjectSubtree(ctx, sourceProjectID, workspaceID, envID)
}

// ProvisionLaneAgentRuntime is refused until a checkpoint records its source
// conversation (design D8). A lane needs its own channel and a per-lane
// env-dispatch binding to derive the executing agent against, and neither can be
// reconstructed from a checkpoint as recorded today: CopyProjectSubtree copies
// issues and chat sessions but not channels. Reusing the source's channel would
// post every lane into one thread, which is the opposite of independent
// continuations, so this fails loudly instead.
//
// The API cannot reach here: standalone fan-out is refused up front for a
// checkpoint with no recorded source conversation, and the branch path
// provisions through provisionEnvDispatchAgentBranch rather than a lane.
func (a *laneMaterializerDepsAdapter) ProvisionLaneAgentRuntime(_ context.Context, in service.LaneAgentProvisionInput) (service.LaneBinding, error) {
	return service.LaneBinding{}, fmt.Errorf(
		"%w: lane %q cannot mint a runtime until checkpoints record their source conversation",
		service.ErrLaneConversationUnavailable, in.LaneKey)
}
