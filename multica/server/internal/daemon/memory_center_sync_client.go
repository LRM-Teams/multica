package daemon

import "context"

func (c *Client) SyncAgentMemoryCenter(ctx context.Context, report AgentMemoryCenterSyncReport) error {
	return c.postJSONWithRetry(ctx, "/api/daemon/agent-memory-center/sync", report, nil, defaultTerminalRetrySchedule)
}

func (c *Client) HydrateAgentMemoryCenter(ctx context.Context, req AgentMemoryHydrateRequest) (AgentMemoryHydrateResponse, error) {
	var resp AgentMemoryHydrateResponse
	if err := c.postJSONWithRetry(ctx, "/api/daemon/agent-memory-center/hydrate", req, &resp, defaultTerminalRetrySchedule); err != nil {
		return AgentMemoryHydrateResponse{}, err
	}
	if resp.Active == nil {
		resp.Active = []AgentMemoryHydrateEntry{}
	}
	if resp.Conflicts == nil {
		resp.Conflicts = []AgentMemoryHydrateEntry{}
	}
	return resp, nil
}
