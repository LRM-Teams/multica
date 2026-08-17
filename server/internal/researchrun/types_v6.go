package researchrun

// Ronaldo V6 storage identities are deliberately separate from the V1-V5
// Task/Attempt model. They are aliases rather than UUID implementations so the
// application boundary remains responsible for parsing untrusted input.
type V6WorkItemKind string
type V6WorkItemStatus string
type V6NodeTier string
type V6DiscussionStatus string
type V6ReportStatus string

const (
	V6WorkResearch    V6WorkItemKind = "research"
	V6WorkMatch       V6WorkItemKind = "match"
	V6WorkDiscussion  V6WorkItemKind = "discussion"
	V6WorkIntegration V6WorkItemKind = "integration"
	V6WorkDirector    V6WorkItemKind = "director"
	V6WorkReport      V6WorkItemKind = "report"
	V6WorkReview      V6WorkItemKind = "review"

	V6TierS   V6NodeTier = "S"
	V6TierM   V6NodeTier = "M"
	V6TierL   V6NodeTier = "L"
	V6TierXL  V6NodeTier = "XL"
	V6TierXXL V6NodeTier = "XXL"
)

type V6WorkItem struct {
	ID, WorkspaceID, RunID, TargetKind, TargetID string
	Kind                                         V6WorkItemKind
	Status                                       V6WorkItemStatus
	GoalVersion                                  int
	StateVersion, EventSequence                  int64
}

type V6WorkItemAttempt struct {
	ID, RunID, WorkItemID, AgentID, MembershipID string
	ManifestID, ManifestHash, DispatchKey        string
	AttemptNumber                                int
	Status                                       V6WorkItemStatus
}

type V6ContentVersionRef struct {
	ArtifactVersionID, ContentHash string
	Tier                           V6NodeTier
}
