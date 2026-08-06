import { beforeEach, describe, expect, it, vi } from "vitest";

const listAgents = vi.fn().mockResolvedValue([]);

vi.mock("../api", () => ({
  api: {
    listAgents: (...args: unknown[]) => listAgents(...args),
  },
}));

import { agentListOptions, workspaceKeys } from "./queries";

describe("agentListOptions (LRM-410)", () => {
  beforeEach(() => {
    listAgents.mockClear();
  });

  it("defaults to excluding archived (no include_archived query param)", async () => {
    const opts = agentListOptions("ws-1");
    expect(opts.queryKey).toEqual(workspaceKeys.agents("ws-1"));
    await opts.queryFn!({} as never);
    expect(listAgents).toHaveBeenCalledWith({ workspace_id: "ws-1" });
    expect(listAgents.mock.calls[0]?.[0]).not.toHaveProperty("include_archived");
  });

  it("opts into include_archived only for archive manage/restore surfaces", async () => {
    const opts = agentListOptions("ws-1", { includeArchived: true });
    expect(opts.queryKey).toEqual([
      ...workspaceKeys.agents("ws-1"),
      "include_archived",
    ]);
    await opts.queryFn!({} as never);
    expect(listAgents).toHaveBeenCalledWith({
      workspace_id: "ws-1",
      include_archived: true,
    });
  });
});
