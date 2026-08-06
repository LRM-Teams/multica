package daemon

import "context"

func (c *Client) ReportAgentMemoryWrites(ctx context.Context, report AgentMemoryWriteReport) error {
	return c.postJSONWithRetry(ctx, "/api/daemon/agent-memory-writes", report, nil, defaultTerminalRetrySchedule)
}
