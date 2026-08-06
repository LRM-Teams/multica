package protocol

// ChannelSubgoalContext is the bounded claim-time view of one sub-goal for the
// claiming agent (LRM-1004). It intentionally excludes other agents' full
// chat/thread history — only purpose, completion boundary, own role, and a
// small activity delta.
type ChannelSubgoalContext struct {
	ID                 string   `json:"id"`
	Title              string   `json:"title"`
	Purpose            string   `json:"purpose"`
	CompletionBoundary string   `json:"completion_boundary,omitempty"`
	Version            int64    `json:"version"`
	Status             string   `json:"status"`
	OwnRole            string   `json:"own_role"` // responsible | participant
	WaitingOnKind      string   `json:"waiting_on_kind,omitempty"`
	WaitingOnNote      string   `json:"waiting_on_note,omitempty"`
	ActivityDelta      []string `json:"activity_delta,omitempty"`
	ArtifactRefs       []string `json:"artifact_refs,omitempty"`
}
