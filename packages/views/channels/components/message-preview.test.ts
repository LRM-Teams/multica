import { describe, expect, it } from "vitest";
import {
  formatChannelMessagePreview,
  resolveChannelAuthorDisplayName,
  type MentionPreviewResolver,
} from "./message-preview";
import { extractEnvelopeParts } from "./message-parts-preview";

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

  it("unwraps a raw structured-action envelope in content when parts are empty (never leaks JSON)", () => {
    const raw =
      '{"action":"message_send","output":"hi","parts":[{"type":"text","text":"hi there"}]}';
    const result = formatChannelMessagePreview("Atlas", raw, resolveMention, []);
    expect(result).toBe("Atlas: hi there");
    expect(result).not.toContain('"action"');
    expect(result).not.toContain("{");
  });

  it("unwraps a raw structured-action envelope when parts arg is undefined", () => {
    const raw =
      '{"action":"message_send","output":"hi","parts":[{"type":"text","text":"hi there"}]}';
    const result = formatChannelMessagePreview("Atlas", raw, resolveMention);
    expect(result).toBe("Atlas: hi there");
    expect(result).not.toContain('"action"');
  });

  it("leaves normal text content unchanged when parts are empty", () => {
    expect(
      formatChannelMessagePreview("Atlas", "just a message", resolveMention, []),
    ).toBe("Atlas: just a message");
  });

  it("falls back to the envelope output when structured parts have no renderable text", () => {
    const raw =
      '{"action":"message_send","output":"fallback summary","parts":[{"type":"image","url":"x"}]}';
    const result = formatChannelMessagePreview("Atlas", raw, resolveMention, []);
    expect(result).toBe("Atlas: fallback summary");
    expect(result).not.toContain('"action"');
    expect(result).not.toContain("{");
  });

  it("never leaks raw JSON when a structured envelope has neither text parts nor output", () => {
    const raw = '{"action":"message_send","parts":[{"type":"image","url":"x"}]}';
    const result = formatChannelMessagePreview("Atlas", raw, resolveMention, []);
    expect(result).not.toContain('"action"');
    expect(result).not.toContain("{");
    expect(result).not.toContain("image");
  });

  it("treats malformed JSON that is not a real envelope as normal text (no crash)", () => {
    const almost = '{"parts": [oops not json';
    const result = formatChannelMessagePreview("Atlas", almost, resolveMention, []);
    expect(result).toBe(`Atlas: ${almost}`);
  });

  it("does not unwrap plain JSON-looking text that has no parts array", () => {
    const jsonish = '{"status":"ok"}';
    expect(
      formatChannelMessagePreview("Atlas", jsonish, resolveMention, []),
    ).toBe(`Atlas: ${jsonish}`);
  });

  // GAP 3 — the discriminator requires a top-level `action` key. A user pasting
  // legit JSON that merely has a `parts` array must render as normal text, not
  // be intercepted and mangled by the envelope unwrap.
  it("does not intercept legit JSON with a parts array but no action key", () => {
    const jsonish = '{"parts":["a","b"]}';
    expect(
      formatChannelMessagePreview("Atlas", jsonish, resolveMention, []),
    ).toBe(`Atlas: ${jsonish}`);
  });
});

describe("extractEnvelopeParts", () => {
  it("returns the envelope parts for a raw structured-action envelope", () => {
    const raw =
      '{"action":"message_send","output":"hi","parts":[{"type":"text","text":"hi there"}]}';
    expect(extractEnvelopeParts(raw)).toEqual([{ type: "text", text: "hi there" }]);
  });

  it("returns an empty array (not null) for an envelope with an empty parts array", () => {
    expect(extractEnvelopeParts('{"action":"message_send","parts":[]}')).toEqual([]);
  });

  // GAP 3 — require the top-level `action` key so legit `{"parts":[...]}` JSON
  // is NOT treated as an envelope (returns null → caller renders it as-is).
  it("returns null for JSON with a parts array but no action key", () => {
    expect(extractEnvelopeParts('{"parts":["a","b"]}')).toBeNull();
  });

  it("returns null for ordinary text and malformed JSON (no crash)", () => {
    expect(extractEnvelopeParts("just a message")).toBeNull();
    expect(extractEnvelopeParts('{"action":"x","parts": [oops')).toBeNull();
  });
});

describe("resolveChannelAuthorDisplayName", () => {
  it("uses display_name from live member identity when only a legacy author_name snapshot is present", () => {
    expect(
      resolveChannelAuthorDisplayName(
        { type: "user", author_name: "andong3" },
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
        { type: "agent", author_id: "agent-1", author_name: "agent_handle" },
        {
          getActorName: (type, id, fallback) =>
            type === "agent" && id === "agent-1" ? "Research Agent" : fallback,
        },
      ),
    ).toBe("Research Agent");
  });
});
