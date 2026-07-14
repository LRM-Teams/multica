package workgraph

const (
	workNodeKindIssue = "issue"

	ownerTypeAgent      = "agent"
	ownerTypeMember     = "member"
	ownerTypeUnassigned = "unassigned"

	workNodeStatusActive    = "active"
	workNodeStatusWaiting   = "waiting"
	workNodeStatusDone      = "done"
	workNodeStatusCancelled = "cancelled"

	issueDependencyBlockedBy = "blocked_by"
	issueDependencyBlocks    = "blocks"
)
