import { describe, expect, it } from "vitest";
import { formatChannelMessagePreview, type MentionPreviewResolver } from "./message-preview";

const resolveMention: MentionPreviewResolver = (type, id, fallback) => {
  if (type === "agent" && id === "agent-1") return "Frontend Engineer";
  if (type === "member" && id === "member-1") return "Frank";
  return fallback;
};

describe("formatChannelMessagePreview", () => {
  it("renders canonical mention markdown as readable display names", () => {
    expect(
      formatChannelMessagePreview(
        "Atlas",
        "please ask [@agent_123](mention://agent/agent-1)",
        resolveMention,
      ),
    ).toBe("Atlas: please ask @Frontend Engineer");
  });

  it("normalizes legacy mention shortcodes before rendering preview text", () => {
    expect(
      formatChannelMessagePreview(
        "Atlas",
        'cc [@ id="member-1" label="frank-an"]',
        resolveMention,
      ),
    ).toBe("Atlas: cc @Frank");
  });

  it("collapses normal markdown links to labels so raw URLs do not leak", () => {
    expect(
      formatChannelMessagePreview(
        "Atlas",
        "see [task](https://example.test/task/1)",
        resolveMention,
      ),
    ).toBe("Atlas: see task");
  });
});
