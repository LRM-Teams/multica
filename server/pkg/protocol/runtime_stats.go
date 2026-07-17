package protocol

// RuntimeTokenStats captures provider-native token/cost/context telemetry for a
// persistent chat session. It is intentionally optional: runtimes that cannot
// report current context usage simply omit it.
type RuntimeTokenStats struct {
	Provider              string   `json:"provider,omitempty"`
	Model                 string   `json:"model,omitempty"`
	InputTokens           int64    `json:"input_tokens,omitempty"`
	OutputTokens          int64    `json:"output_tokens,omitempty"`
	CacheReadTokens       int64    `json:"cache_read_tokens,omitempty"`
	CacheWriteTokens      int64    `json:"cache_write_tokens,omitempty"`
	TotalTokens           int64    `json:"total_tokens,omitempty"`
	CostUSD               *float64 `json:"cost_usd,omitempty"`
	ContextTokens         *int64   `json:"context_tokens,omitempty"`
	ContextWindow         *int64   `json:"context_window,omitempty"`
	ContextPercent        *float64 `json:"context_percent,omitempty"`
	AutoCompactionEnabled *bool    `json:"auto_compaction_enabled,omitempty"`
	UpdatedAt             string   `json:"updated_at,omitempty"`
}
