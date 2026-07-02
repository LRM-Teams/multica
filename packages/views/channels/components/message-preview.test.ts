import { describe, expect, it } from "vitest";
import {
  formatChannelMessagePreview,
  resolveChannelAuthorDisplayName,
  type MentionPreviewResolver,
} from "./message-preview";

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

  it("prefers structured message parts over fallback content", () => {
    expect(
      formatChannelMessagePreview(
        "Atlas",
        ":sticker:hi:",
        resolveMention,
        [{ type: "sticker", sticker_id: "hi", alt: "Hi sticker" }],
      ),
    ).toBe("Atlas: [Sticker] Hi sticker");
  });

  it("does not leak sticker ids when previewing unknown structured sticker parts", () => {
    expect(
      formatChannelMessagePreview(
        "Atlas",
        ":sticker:internal-id:",
        resolveMention,
        [{ type: "sticker", sticker_id: "internal-id" }],
      ),
    ).toBe("Atlas: [Sticker]");
  });
});

describe("resolveChannelAuthorDisplayName", () => {
  it("uses display_name from live member identity when only a legacy author_name snapshot is present", () => {
    expect(
      resolveChannelAuthorDisplayName(
        { author_type: "user", author_name: "andong3" },
        {
          members: [
            {
              id: "member-1",
              workspace_id: "ws-1",
              user_id: "user-1",
              role: "owner",
              created_at: "2026-07-01T00:00:00Z",
              name: "andong3",
              display_name: "Frank An",
              email: "frank@example.test",
              avatar_url: null,
              profile_description: "",
            },
          ],
        },
      ),
    ).toBe("Frank An");
  });

  it("uses the actor-name resolver by id before falling back to the message snapshot", () => {
    expect(
      resolveChannelAuthorDisplayName(
        { author_type: "agent", author_id: "agent-1", author_name: "agent_handle" },
        {
          getActorName: (type, id, fallback) =>
            type === "agent" && id === "agent-1" ? "Research Agent" : fallback,
        },
      ),
    ).toBe("Research Agent");
  });
});
