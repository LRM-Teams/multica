// SPDX-License-Identifier: Apache-2.0

package service

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5/pgtype"

	"github.com/multica-ai/multica/server/internal/util"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

// inFlightTaskQueries is the one query the resolver needs. It is not
// workspace-scoped: the join reaches the project through issue or chat_session,
// and a project belongs to exactly one workspace, so the project id alone is
// already a workspace-scoped filter.
type inFlightTaskQueries interface {
	ListInFlightTasksForProject(ctx context.Context, projectID pgtype.UUID) ([]db.AgentInboxEvent, error)
}

type inFlightTaskResolver struct {
	q inFlightTaskQueries
}

// NewInFlightTaskResolver builds the production InFlightTaskResolver, which
// captures what a checkpoint needs to re-engage the agent later.
func NewInFlightTaskResolver(q inFlightTaskQueries) InFlightTaskResolver {
	return &inFlightTaskResolver{q: q}
}

func (r *inFlightTaskResolver) ListInFlightTasksForProject(ctx context.Context, workspaceID, projectID string) ([]ResumeTrigger, error) {
	if _, err := util.ParseUUID(workspaceID); err != nil {
		return nil, fmt.Errorf("invalid workspace_id: %w", err)
	}
	projectUUID, err := util.ParseUUID(projectID)
	if err != nil {
		return nil, fmt.Errorf("invalid project_id: %w", err)
	}
	rows, err := r.q.ListInFlightTasksForProject(ctx, projectUUID)
	if err != nil {
		return nil, fmt.Errorf("list in-flight tasks for project %s: %w", projectID, err)
	}
	triggers := make([]ResumeTrigger, 0, len(rows))
	for _, row := range rows {
		trigger, ok := resumeTriggerFromInboxEvent(row, projectID)
		if !ok {
			continue
		}
		triggers = append(triggers, trigger)
	}
	return triggers, nil
}

// resumeTriggerFromInboxEvent converts one in-flight row, reporting false for a
// row that could never be resumed. Continuation resets the task by (task id,
// runtime id) and the resumed agent continues an issue or a chat session, so a
// row missing any of those is dropped at capture time rather than stored as a
// trigger that only fails once someone tries to resume it.
func resumeTriggerFromInboxEvent(row db.AgentInboxEvent, projectID string) (ResumeTrigger, bool) {
	if !row.ID.Valid || !row.RuntimeID.Valid || !row.AgentID.Valid {
		return ResumeTrigger{}, false
	}
	trigger := ResumeTrigger{
		TaskID:    util.UUIDToString(row.ID),
		RuntimeID: util.UUIDToString(row.RuntimeID),
		AgentID:   util.UUIDToString(row.AgentID),
		ProjectID: projectID,
	}
	switch {
	case row.IssueID.Valid:
		trigger.IssueID = util.UUIDToString(row.IssueID)
		trigger.Kind = "issue"
	case row.ChatSessionID.Valid:
		trigger.ChatSessionID = util.UUIDToString(row.ChatSessionID)
		trigger.Kind = "chat"
	default:
		return ResumeTrigger{}, false
	}
	return trigger, true
}

// sandboxLifecycleJobs is the pause/resume half of EnvSandboxLifecycleService.
// The checkpoint seams only care whether the job was accepted, so the job
// descriptor is dropped here rather than widening the seams to carry it.
type sandboxLifecycleJobs interface {
	Save(ctx context.Context, ref SandboxInstanceRef, actorUserID string) (SandboxLifecycleJobResult, error)
	Resume(ctx context.Context, ref SandboxInstanceRef, actorUserID string) (SandboxLifecycleJobResult, error)
}

type sandboxInstanceSaver struct {
	jobs sandboxLifecycleJobs
}

// NewSandboxInstanceSaver adapts the lifecycle service to the pause_in_place
// save seam.
func NewSandboxInstanceSaver(jobs sandboxLifecycleJobs) SandboxInstanceSaver {
	return &sandboxInstanceSaver{jobs: jobs}
}

func (s *sandboxInstanceSaver) Save(ctx context.Context, ref SandboxInstanceRef, actorUserID string) error {
	// The error is what records the checkpoint as failed or timed out. Dropping
	// it would mark a checkpoint complete over an instance that is still
	// running, so the checkpoint would claim state it never captured.
	if _, err := s.jobs.Save(ctx, ref, actorUserID); err != nil {
		return fmt.Errorf("save sandbox instance %s: %w", ref.InstanceID, err)
	}
	return nil
}

type sandboxInstanceResumer struct {
	jobs sandboxLifecycleJobs
}

// NewSandboxInstanceResumer adapts the lifecycle service to the resume seam.
func NewSandboxInstanceResumer(jobs sandboxLifecycleJobs) SandboxInstanceResumer {
	return &sandboxInstanceResumer{jobs: jobs}
}

func (s *sandboxInstanceResumer) Resume(ctx context.Context, ref SandboxInstanceRef, actorUserID string) error {
	if _, err := s.jobs.Resume(ctx, ref, actorUserID); err != nil {
		return fmt.Errorf("resume sandbox instance %s: %w", ref.InstanceID, err)
	}
	return nil
}
