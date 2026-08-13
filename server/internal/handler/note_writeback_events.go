package handler

// Note writeback subscription (S3-W1) and event whitelist (S3-W2).
//
// Subscription strategy: "linked means subscribed".
// Any row in note_page_issue_ref is an implicit subscription from that note to
// the issue. There is no separate subscribe table in MVP — creating/removing a
// ref is how users opt in/out. Do not invent auto-subscribe from mentions alone
// without a ref row.
//
// Event whitelist (S3-W2): only terminal issue status transitions produce
// pending note writebacks. Noise (in_progress, blocked, title edits, ordinary
// comments) must never create proposals. Key-comment writebacks are deferred.

// noteWritebackIssueEvent is a whitelisted issue→note writeback trigger.
type noteWritebackIssueEvent string

const (
	noteWritebackIssueDone      noteWritebackIssueEvent = "done"
	noteWritebackIssueCancelled noteWritebackIssueEvent = "cancelled"
)

// noteWritebackIssueEventWhitelist is the closed set of issue statuses that may
// produce a pending note writeback when newly entered (prev != next).
var noteWritebackIssueEventWhitelist = map[string]noteWritebackIssueEvent{
	"done":      noteWritebackIssueDone,
	"cancelled": noteWritebackIssueCancelled,
}

// classifyNoteWritebackIssueTransition returns the whitelist event when an
// issue status change should propose note writebacks. Re-entering the same
// terminal status (prev == next) yields ok=false.
func classifyNoteWritebackIssueTransition(prevStatus, nextStatus string) (noteWritebackIssueEvent, bool) {
	if prevStatus == nextStatus {
		return "", false
	}
	event, ok := noteWritebackIssueEventWhitelist[nextStatus]
	return event, ok
}
