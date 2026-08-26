import { describe, expect, it } from "vitest";
import { canManageChannelGoal } from "./can-manage-channel-goal";

const general = { created_by: "creator-1", system_key: "general" as const };
const group = { created_by: "creator-1" };

describe("canManageChannelGoal", () => {
  it("allows the channel creator, including on #general", () => {
    expect(canManageChannelGoal(general, "creator-1", "member")).toBe(true);
    expect(canManageChannelGoal(group, "creator-1", "member")).toBe(true);
  });

  it("allows workspace owner and admin who did not create the channel", () => {
    expect(canManageChannelGoal(general, "user-2", "owner")).toBe(true);
    expect(canManageChannelGoal(group, "user-2", "admin")).toBe(true);
  });

  it("hides Goal setup from ordinary members", () => {
    expect(canManageChannelGoal(general, "user-2", "member")).toBe(false);
    expect(canManageChannelGoal(group, null, "owner")).toBe(false);
  });
});
