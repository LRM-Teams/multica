// SPDX-License-Identifier: Apache-2.0

package service

import (
	"github.com/jackc/pgx/v5/pgtype"

	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

// OwnPinnedSession returns the session and work dir a claim should continue from
// the task row's own pinned resume pointer, or empty strings if there is nothing
// to continue.
//
// This exists for pause_in_place resume. ResetInFlightTaskForResume re-activates
// the *same* task row and preserves its session_id, but the cross-task lookups
// (GetLastTaskSession, GetLastChatTaskSession) only match terminal tasks -- they
// answer "did an earlier task on this issue leave a session behind", and a
// resumed task is neither earlier nor terminal. So the session sitting on the
// row being claimed is invisible to them, and without reading it a resume starts
// cold even though the transcript is still on the preserved disk.
//
// It is also what keeps forked lanes cold: a lane's task row is new and has no
// session_id, and its runtime is its own, so neither this nor the cross-task
// lookups can hand it the source's session. N lanes resuming one mutable session
// file under N runtimes is the failure this avoids.
//
// Three guards, each load-bearing:
//   - Non-terminal only. A terminal row's session belongs to the cross-task
//     lookups, which filter out poisoned outcomes (iteration_limit,
//     api_invalid_request, ...) before resuming; reading it here would bypass
//     that filter.
//   - Runtime match. A session file is tied to the disk and process that wrote
//     it, so it is only resumable by the same runtime.
//   - force_fresh_session wins. It is a deliberate instruction that the prior
//     conversation is bad, so it must beat the row's own session too.
func OwnPinnedSession(event db.AgentInboxEvent, claimingRuntimeID pgtype.UUID) (sessionID, workDir string) {
	if event.ForceFreshSession {
		return "", ""
	}
	switch event.Status {
	case "pending", "draining":
	default:
		return "", ""
	}
	if !claimingRuntimeID.Valid || event.RuntimeID != claimingRuntimeID {
		return "", ""
	}
	if event.SessionID.Valid {
		sessionID = event.SessionID.String
	}
	// A work dir without a session is still worth continuing: the checkout is on
	// the preserved disk even when the agent never pinned a session.
	if event.WorkDir.Valid {
		workDir = event.WorkDir.String
	}
	return sessionID, workDir
}
