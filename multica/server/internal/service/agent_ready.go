package service

import (
	"context"
	"time"

	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

// AgentReadiness reports whether an agent can accept new work right now.
// "Ready" means archived_at IS NULL, runtime_id IS NOT NULL, and the bound
// runtime is currently reachable (RuntimeConnectivity == online — not just
// status == 'online', which can lag reality by up to ~180s; see #53). When
// not ready, reason describes which gate failed in language suitable for
// autopilot_run.failure_reason.
//
// err is non-nil only on DB lookup failure for the runtime row. Callers that
// treat a transient DB error as "do not skip" (the autopilot admission gate)
// should swallow it; callers that need a hard yes/no should fail closed.
//
// This is the single source of truth shared by:
//   - service.shouldSkipDispatch (autopilot admission gate)
//   - service.dispatchRunOnly    (autopilot runtime check)
//
// Task #53 correction: the doc comment here previously also listed
// service.enqueueChatTask as a consumer. Verified against origin/dev:
// enqueueChatTask never calls this function (createChatTaskRow only checks
// ArchivedAt/RuntimeID.Valid, no runtime liveness check at all) — it was
// never a real caller. Listing it here was misleading: fixing this function
// alone does not fix chat-task admission liveness (that gap is untouched,
// tracked separately, not in #53's scope). The squad-leader path this
// comment used to also mention (handler.isSquadLeaderReady) no longer
// exists — deleted outright by the squad-leader retirement (#1650).
//
// Keeping the two real callers aligned matters because they can otherwise
// drift — e.g. one starts allowing "stale" runtimes while the other
// doesn't, and the bug only surfaces when a user hits two different entry
// points for the same agent. Touch this function, both paths move together.
func AgentReadiness(ctx context.Context, q *db.Queries, agent db.Agent) (ready bool, reason string, err error) {
	if agent.ArchivedAt.Valid {
		return false, "agent is archived", nil
	}
	if !agent.RuntimeID.Valid {
		return false, "agent has no runtime bound", nil
	}
	rt, err := q.GetAgentRuntime(ctx, agent.RuntimeID)
	if err != nil {
		return false, "", err
	}
	if RuntimeConnectivity(rt, time.Now()) != RuntimeConnectivityOnline {
		return false, "agent runtime is " + rt.Status, nil
	}
	return true, "", nil
}
