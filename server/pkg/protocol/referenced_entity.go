package protocol

// ReferencedEntitySnapshot is bounded, read-only context resolved from a
// canonical mention://issue or mention://agent link in the triggering text.
// Content is a single normalized display line; Type and ID keep the wire
// auditable without changing the original message body or reference anchor.
type ReferencedEntitySnapshot struct {
	Type    string `json:"type"`
	ID      string `json:"id"`
	Content string `json:"content"`
}
