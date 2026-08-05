package researchrun

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
)

type resultSubmission struct {
	SessionID   string
	WorkspaceID string
	TaskID      string
	AttemptID   string
	AgentID     string
	InboxTaskID string
	Raw         json.RawMessage
}

// resultAcceptanceStore is the persistence input used before and during one
// result acceptance. PostgresStore performs the final identity, state, replay,
// and materialization checks atomically inside AcceptResult.
type resultAcceptanceStore interface {
	GetRun(context.Context, string, string) (Run, error)
	GetTask(context.Context, string, string) (Task, error)
	ListAttempts(context.Context, string) ([]Attempt, error)
	ListFleetMembers(context.Context, string, string) ([]FleetMember, error)
	AcceptResult(context.Context, AcceptResultInput) (AcceptResultOutcome, error)
}

type resultAcceptanceModule struct {
	store resultAcceptanceStore
}

func (module resultAcceptanceModule) Accept(ctx context.Context, submission resultSubmission) (AcceptResultOutcome, error) {
	run, err := module.store.GetRun(ctx, submission.SessionID, submission.WorkspaceID)
	if err != nil {
		return AcceptResultOutcome{}, err
	}
	task, err := module.store.GetTask(ctx, submission.TaskID, submission.SessionID)
	if err != nil {
		return AcceptResultOutcome{}, err
	}
	attempts, err := module.store.ListAttempts(ctx, submission.SessionID)
	if err != nil {
		return AcceptResultOutcome{}, err
	}
	found := false
	for _, attempt := range attempts {
		if attempt.ID == submission.AttemptID && attempt.TaskID == submission.TaskID {
			found = true
			if attempt.InboxTaskID == "" || attempt.InboxTaskID != submission.InboxTaskID {
				return AcceptResultOutcome{}, ErrAttemptNotAssigned
			}
			break
		}
	}
	if !found {
		return AcceptResultOutcome{}, ErrRunNotFound
	}
	result, hash, err := DecodeAndValidateResultForVersion(run.OrchestratorVersion, submission.Raw, task, run.Config)
	if err != nil {
		return AcceptResultOutcome{}, err
	}
	members, err := module.store.ListFleetMembers(ctx, submission.SessionID, submission.WorkspaceID)
	if err != nil {
		return AcceptResultOutcome{}, err
	}
	if missing := missingResultCapabilities(result, members); len(missing) > 0 {
		return AcceptResultOutcome{}, fmt.Errorf(
			"%w: no active fleet member has role(s) %s; the lead must hire, optimize, and activate those specialties before retrying the same result",
			ErrCapabilityUnavailable,
			strings.Join(missing, ", "),
		)
	}
	return module.store.AcceptResult(ctx, AcceptResultInput{
		SessionID: submission.SessionID, AttemptID: submission.AttemptID,
		AgentID: submission.AgentID, InboxTaskID: submission.InboxTaskID,
		Raw: submission.Raw, Result: result, Hash: hash,
	})
}

func missingResultCapabilities(result ResultEnvelope, members []FleetMember) []string {
	active := map[string]struct{}{}
	for _, member := range members {
		if member.Status == "active" {
			active[strings.ToLower(strings.TrimSpace(member.Role))] = struct{}{}
		}
	}
	missing := map[string]struct{}{}
	check := func(tasks []TaskProposal) {
		for _, task := range tasks {
			capability := strings.ToLower(strings.TrimSpace(task.RequiredCapability))
			if _, ok := active[capability]; !ok {
				missing[capability] = struct{}{}
			}
		}
	}
	if result.Plan != nil {
		check(result.Plan.Tasks)
	}
	check(result.ProposedTasks)
	out := make([]string, 0, len(missing))
	for capability := range missing {
		out = append(out, capability)
	}
	sort.Strings(out)
	return out
}
