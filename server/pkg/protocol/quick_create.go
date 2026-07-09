package protocol

// QuickCreateSourceContext is the safe, bounded chat/thread context carried
// from a chat surface into a quick-create task. It is deliberately made of
// user-visible identifiers and excerpts only: no queue ids, run internals, or
// hidden event payloads.
type QuickCreateSourceContext struct {
	ChannelID           string   `json:"channel_id"`
	ChannelKind         string   `json:"channel_kind"`
	ChannelName         string   `json:"channel_name,omitempty"`
	ThreadRootMessageID string   `json:"thread_root_message_id"`
	SourceMessageID     string   `json:"source_message_id"`
	SourceAuthorType    string   `json:"source_author_type,omitempty"`
	SourceAuthorID      string   `json:"source_author_id,omitempty"`
	SourceAuthorName    string   `json:"source_author_name,omitempty"`
	SourceExcerpt       string   `json:"source_excerpt,omitempty"`
	Summary             string   `json:"summary,omitempty"`
	AttachmentIDs       []string `json:"attachment_ids,omitempty"`
}
