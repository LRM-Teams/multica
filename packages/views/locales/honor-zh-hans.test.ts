// @vitest-environment node

import { describe, expect, it } from "vitest";
import zhAgents from "./zh-Hans/agents.json";
import zhChannels from "./zh-Hans/channels.json";
import zhCommon from "./zh-Hans/common.json";
import zhMembers from "./zh-Hans/members.json";
import zhSettings from "./zh-Hans/settings.json";

describe("Simplified Chinese honor copy", () => {
  it("does not expose English experience or level abbreviations", () => {
    const honorCopy = JSON.stringify([
      zhSettings.honor,
      zhAgents.honor_agent,
      {
        stats: zhAgents.side_panel.honor_stats,
        next: zhAgents.side_panel.honor_xp_to_next,
      },
      zhMembers.panel.honor_stats,
      zhChannels.profile_popover.honor,
      zhCommon.honor_level_value,
    ]);

    expect(honorCopy).not.toMatch(/\bXP\b|LV\.|Lv\.|Builder/);
  });
});
