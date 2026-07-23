import { describe, expect, it } from "vitest";
import type { Agent } from "../types";
import { filterAgentsForChannelInvite } from "./channel-invite-filter";

function agent(partial: Partial<Agent> & Pick<Agent, "id" | "visibility">): Agent {
  return {
    name: partial.id,
    workspace_id: "ws",
    owner_id: "u1",
    runtime: "cursor",
    model: "auto",
    instructions: "",
    created_at: "",
    updated_at: "",
    ...partial,
  } as Agent;
}

describe("filterAgentsForChannelInvite (LRM-399)", () => {
  it("keeps workspace/private agents and this channel's channel agents", () => {
    const list = [
      agent({ id: "ws", visibility: "workspace" }),
      agent({ id: "priv", visibility: "private" }),
      agent({ id: "home", visibility: "channel", home_channel_id: "ch-c" }),
      agent({ id: "other", visibility: "channel", home_channel_id: "ch-other" }),
      agent({ id: "orphan", visibility: "channel", home_channel_id: null }),
      agent({ id: "arch", visibility: "workspace", archived_at: "2026-01-01" }),
    ];
    const out = filterAgentsForChannelInvite(list, "ch-c");
    expect(out.map((a) => a.id)).toEqual(["ws", "priv", "home"]);
  });

  it("returns empty when channel id is blank", () => {
    expect(
      filterAgentsForChannelInvite(
        [agent({ id: "ws", visibility: "workspace" })],
        "  ",
      ),
    ).toEqual([]);
  });
});
