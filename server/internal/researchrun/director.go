package researchrun

import (
	"context"
	"errors"
	"fmt"
	"strings"
)

var ErrV6DirectorUnavailable = errors.New("research V6 director unavailable")

type V6DirectorAssignment struct {
	ID           string `json:"id"`
	WorkspaceID  string `json:"workspace_id"`
	RunID        string `json:"run_id"`
	AgentID      string `json:"director_agent_id"`
	Status       string `json:"status"`
	Reason       string `json:"reason"`
	Generation   int    `json:"generation"`
	StateVersion int64  `json:"state_version"`
}

type AssignV6DirectorInput struct {
	WorkspaceID, RunID, AgentID, UserID, Reason, ClientRequestID string
	ExpectedStateVersion                                         int64
}

type MarkV6DirectorUnavailableInput struct {
	WorkspaceID, RunID, AssignmentID, FailureClass, Diagnostics string
	ExpectedStateVersion                                        int64
}

type directorV6Store interface {
	AssignV6Director(context.Context, AssignV6DirectorInput) (V6DirectorAssignment, error)
	MarkV6DirectorUnavailable(context.Context, MarkV6DirectorUnavailableInput) (V6DirectorAssignment, error)
}

type directorModule struct{ store directorV6Store }

func (m directorModule) Assign(ctx context.Context, in AssignV6DirectorInput) (V6DirectorAssignment, error) {
	if m.store == nil || strings.TrimSpace(in.AgentID) == "" || strings.TrimSpace(in.UserID) == "" || strings.TrimSpace(in.Reason) == "" || strings.TrimSpace(in.ClientRequestID) == "" || in.ExpectedStateVersion < 0 {
		return V6DirectorAssignment{}, fmt.Errorf("%w: incomplete Director assignment", ErrInvalidContract)
	}
	return m.store.AssignV6Director(ctx, in)
}

func (m directorModule) MarkUnavailable(ctx context.Context, in MarkV6DirectorUnavailableInput) (V6DirectorAssignment, error) {
	if m.store == nil || strings.TrimSpace(in.AssignmentID) == "" || strings.TrimSpace(in.FailureClass) == "" {
		return V6DirectorAssignment{}, fmt.Errorf("%w: Director failure is incomplete", ErrInvalidContract)
	}
	return m.store.MarkV6DirectorUnavailable(ctx, in)
}

type DirectorModelAdapter interface {
	RunDirectorCycle(context.Context, V6DirectorAssignment, V6WorkManifest) ([]byte, string, error)
}
