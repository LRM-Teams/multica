package protocol

// IssueAssignmentSnapshot is the assignment context delivered at claim time.
// The content fields and comment count are frozen when the wake is enqueued;
// Status is copied from the claim-time current issue. Comment bodies
// deliberately stay out of this payload.
type IssueAssignmentSnapshot struct {
	Version            int            `json:"version"`
	Title              string         `json:"title"`
	Description        *string        `json:"description"`
	AcceptanceCriteria []string       `json:"acceptance_criteria"`
	Status             string         `json:"status"` // current at claim time, not frozen at enqueue
	Metadata           map[string]any `json:"metadata"`
	CommentCount       int64          `json:"comment_count"`
}

func (s IssueAssignmentSnapshot) IsTerminal() bool {
	return s.Status == "done" || s.Status == "cancelled"
}
