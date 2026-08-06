// @vitest-environment node
import { describe, it, expect } from "vitest";
import { AGENT_AVATAR_PRESETS } from "@multica/core/workspace/avatar-url";
import {
  randomAgentAvatarPresetUrl,
  randomPickedAvatarSelection,
} from "./avatar-preset";

describe("avatar-preset helpers", () => {
  it("picks a canonical preset path", () => {
    const url = randomAgentAvatarPresetUrl(() => 0);
    expect(AGENT_AVATAR_PRESETS).toContain(url);
    expect(url).toBe(AGENT_AVATAR_PRESETS[0]);
  });

  it("builds a picked avatar_selection override", () => {
    const selection = randomPickedAvatarSelection(() => 0.99);
    expect(selection.kind).toBe("picked");
    expect(AGENT_AVATAR_PRESETS).toContain(selection.preset_url);
  });
});
