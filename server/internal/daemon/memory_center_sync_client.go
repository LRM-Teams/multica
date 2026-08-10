package daemon

import "context"

func (c *Client) SyncAgentMemoryCenter(ctx context.Context, report AgentMemoryCenterSyncReport) (AgentMemoryCenterSyncResponse, error) {
	var resp AgentMemoryCenterSyncResponse
	if err := c.postJSONWithRetryToken(ctx, "/api/daemon/agent-memory-center/sync", report, &resp, defaultTerminalRetrySchedule, c.tokenForRuntime(report.RuntimeID)); err != nil {
		return AgentMemoryCenterSyncResponse{}, err
	}
	return resp, nil
}

func (c *Client) HydrateAgentMemoryCenter(ctx context.Context, req AgentMemoryHydrateRequest) (AgentMemoryHydrateResponse, error) {
	var resp AgentMemoryHydrateResponse
	if err := c.postJSONWithRetryToken(ctx, "/api/daemon/agent-memory-center/hydrate", req, &resp, defaultTerminalRetrySchedule, c.tokenForRuntime(req.RuntimeID)); err != nil {
		return AgentMemoryHydrateResponse{}, err
	}
	if resp.Active == nil {
		resp.Active = []AgentMemoryHydrateEntry{}
	}
	if resp.Conflicts == nil {
		resp.Conflicts = []AgentMemoryHydrateEntry{}
	}
	if resp.Deleted == nil {
		resp.Deleted = []AgentMemoryHydrateEntry{}
	}
	return resp, nil
}
