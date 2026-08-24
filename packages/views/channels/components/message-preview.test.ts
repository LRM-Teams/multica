// @vitest-environment node
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

  it("resolves a bare @handle anchored by a reference span (#530)", () => {
    // Frank: the channel-list preview showed `@actor_14` while the body said `@小雅`.
    // Under #463 a mention message carries `parts: [reference]` with a SPAN into
    // `content` and NO text part — `formatMessagePartsPreview` understands only
    // text/sticker, so it produced nothing and the caller fell back to raw content,
    // internal handle and all. That helper predates #463; `parts` changed meaning
    // underneath it.
    expect(
      formatChannelMessagePreview("Atlas", "cc @agent_123 pls", resolveMention, [
        {
          type: "reference",
          ref_type: "mention",
          ref_subtype: "agent",
          ref_id: "agent-1",
          label: "@agent_123",
          content_start_utf16: 3,
          content_end_utf16: 13,
        },
      ] as never),
    ).toBe("Atlas: cc @Frontend Engineer pls");
  });

  it("keeps an issue ref's span substring verbatim in the preview (#530)", () => {
    // The projector decorates; it never rewrites the author's words (#467/#600).
    // Only mentions resolve to a live display name — the same rule the body follows.
    expect(
      formatChannelMessagePreview("Atlas", "fix MUL-9 today", resolveMention, [
        {
          type: "reference",
          ref_type: "issue-ref",
          ref_subtype: "issue",
          ref_id: "issue-uuid",
          label: "MUL-9",
          content_start_utf16: 4,
          content_end_utf16: 9,
        },
      ] as never),
    ).toBe("Atlas: fix MUL-9 today");
  });

  it("collapses a channel-ref to a #label, never leaking the raw markdown link (task #912)", () => {
    // Unlike issue-ref, a channel-ref is ALWAYS authored via the composer's
    // `[team-a](mention://channel/<id>)` markdown link — there is no bare-text
    // form — so its anchored span covers the WHOLE link syntax. Falling
    // through to the "verbatim span substring" rule (like issue-ref above)
    // would leak `[team-a](mention://channel/channel-uuid)` — internal UUID
    // and all — into the sidebar preview.
    const raw = "[team-a](mention://channel/channel-uuid)";
    expect(
      formatChannelMessagePreview("Atlas", `see ${raw} for context`, resolveMention, [
        {
          type: "reference",
          ref_type: "channel-ref",
          ref_id: "channel-uuid",
          label: "team-a",
          content_start_utf16: 4,
          content_end_utf16: 4 + raw.length,
        },
      ] as never),
    ).toBe("Atlas: see #team-a for context");
  });

  it("still renders plain text when nothing is anchored — the control (#530)", () => {
    // Without this, a projection that returned "" for everything would make the
    // leak tests pass while destroying every ordinary preview.
    expect(formatChannelMessagePreview("Atlas", "just a normal line", resolveMention, [])).toBe(
      "Atlas: just a normal line",
    );
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

  it("summarizes a voice message without leaking its hidden transcript", () => {
    const transcript = "private spoken transcript";
    const result = formatChannelMessagePreview(
      "Wendy",
      transcript,
      resolveMention,
      [
        { type: "text", text: transcript },
        { type: "voice", duration_ms: 2400 },
      ],
      {
        formatVoice: (seconds) => `语音消息 · ${seconds} 秒`,
      },
    );

    expect(result).toBe("Wendy: 语音消息 · 2 秒");
    expect(result).not.toContain(transcript);
  });

  it("uses a safe generic voice summary when a reading surface omits localization", () => {
    const transcript = "another hidden transcript";
    const result = formatChannelMessagePreview(
      "Wendy",
      transcript,
      resolveMention,
      [{ type: "text", text: transcript }, { type: "voice" }],
    );

    expect(result).toBe("Wendy: Voice message");
    expect(result).not.toContain(transcript);
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

  // #250 — the real leaking historical content is reasoning text prefix + the
  // envelope JSON concatenated, so a whole-string parse throws. The preview must
  // still surface only the agent's intended message, never the prefix or JSON.
  it("unwraps a text-prefixed embedded envelope, dropping the reasoning prefix and raw JSON", () => {
    const raw =
      'Repo isn\'t checked out this turn either — consistent with prior. {"action":"message_send","output":"x","parts":[{"type":"text","text":"the real message"}]}';
    const result = formatChannelMessagePreview("Atlas", raw, resolveMention, []);
    expect(result).toBe("Atlas: the real message");
    expect(result).not.toContain('"action"');
    expect(result).not.toContain("Repo isn't checked out");
    expect(result).not.toContain("{");
  });

  it("extracts an embedded envelope even when a part text value contains braces", () => {
    const raw =
      'thinking about it… {"action":"message_send","output":"y","parts":[{"type":"text","text":"use {curly} and }brace{ here"}]}';
    const result = formatChannelMessagePreview("Atlas", raw, resolveMention, []);
    expect(result).toBe("Atlas: use {curly} and }brace{ here");
    expect(result).not.toContain('"action"');
    expect(result).not.toContain("thinking about it");
  });

  it("leaves prose that merely mentions a stray JSON object unchanged", () => {
    const prose = "here is {\"foo\":1} in my note";
    expect(
      formatChannelMessagePreview("Atlas", prose, resolveMention, []),
    ).toBe(`Atlas: ${prose}`);
  });

  it("falls back to the embedded envelope output when it carries no text parts", () => {
    const raw =
      'reasoning prefix here {"action":"message_send","output":"fallback summary","parts":[{"type":"image","url":"x"}]}';
    const result = formatChannelMessagePreview("Atlas", raw, resolveMention, []);
    expect(result).toBe("Atlas: fallback summary");
    expect(result).not.toContain('"action"');
    expect(result).not.toContain("reasoning prefix");
    expect(result).not.toContain("{");
  });

  // GAP 1 — an EARLIER non-message_send action envelope (e.g. a tool-call) must
  // not shadow the later real message_send envelope. First-match-wins used to
  // return the first `{"action":...}` object and drop the real message.
  it("skips an earlier non-message_send envelope and unwraps the later message_send one", () => {
    const raw =
      'x {"action":"a","parts":[]} y {"action":"message_send","parts":[{"type":"text","text":"real"}]}';
    const result = formatChannelMessagePreview("Atlas", raw, resolveMention, []);
    expect(result).toBe("Atlas: real");
    expect(result).not.toContain('"action"');
    expect(result).not.toContain("{");
    expect(result).not.toBe("Atlas: …");
  });

  // GAP 1b — among MULTIPLE message_send envelopes, prefer the one whose parts
  // yield renderable text over an earlier empty-parts one.
  it("prefers the message_send envelope with renderable text over an earlier empty one", () => {
    const raw =
      'prefix {"action":"message_send","parts":[]} then {"action":"message_send","parts":[{"type":"text","text":"the text one"}]}';
    const result = formatChannelMessagePreview("Atlas", raw, resolveMention, []);
    expect(result).toBe("Atlas: the text one");
    expect(result).not.toContain('"action"');
    expect(result).not.toContain("{");
    expect(result).not.toBe("Atlas: …");
  });

  // GAP 2 — prose that merely embeds a NON-message_send action object must
  // render UNCHANGED, never be intercepted and blanked.
  it("leaves prose embedding a non-message_send action object unchanged", () => {
    const prose = 'talking about {"action":"navigate","parts":["home"]} in my config';
    const result = formatChannelMessagePreview("Atlas", prose, resolveMention, []);
    expect(result).toBe(`Atlas: ${prose}`);
    expect(result).not.toBe("Atlas: …");
  });

  // GAP 2 — a PURE non-message_send envelope with no prefix is also not a
  // message to unwrap; it stays as text rather than being blanked.
  it("does not unwrap a pure non-message_send action envelope", () => {
    const raw = '{"action":"navigate","parts":["home"]}';
    const result = formatChannelMessagePreview("Atlas", raw, resolveMention, []);
    expect(result).toBe(`Atlas: ${raw}`);
    expect(result).not.toBe("Atlas: …");
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

  // #250 — embedded envelope: reasoning-text prefix concatenated ahead of the
  // envelope JSON. The whole-string parse throws, so a brace-matched scan must
  // locate the envelope and return its real parts.
  it("extracts parts from a text-prefixed embedded envelope", () => {
    const raw =
      'Repo isn\'t checked out. {"action":"message_send","output":"x","parts":[{"type":"text","text":"the real message"}]}';
    expect(extractEnvelopeParts(raw)).toEqual([
      { type: "text", text: "the real message" },
    ]);
  });

  it("extracts an embedded envelope whose part text contains braces", () => {
    const raw =
      'prefix {"action":"message_send","parts":[{"type":"text","text":"a { b } c"}]} suffix';
    expect(extractEnvelopeParts(raw)).toEqual([{ type: "text", text: "a { b } c" }]);
  });

  // GAP 3 — an embedded object without a top-level `action` key (or prose that
  // merely mentions JSON) must NOT be intercepted.
  it("returns null for prose mentioning a stray JSON object without an action envelope", () => {
    expect(extractEnvelopeParts('here is {"foo":1} in my note')).toBeNull();
    expect(extractEnvelopeParts('note: {"parts":["a","b"]} pasted')).toBeNull();
  });

  // GAP 1 — skip an earlier non-message_send envelope and return the real
  // message_send parts.
  it("skips an earlier non-message_send envelope and returns the message_send parts", () => {
    const raw =
      'x {"action":"a","parts":[]} y {"action":"message_send","parts":[{"type":"text","text":"real"}]}';
    expect(extractEnvelopeParts(raw)).toEqual([{ type: "text", text: "real" }]);
  });

  // GAP 1b — among message_send envelopes, prefer the one with renderable text.
  it("prefers the message_send envelope whose parts have text over an earlier empty one", () => {
    const raw =
      'a {"action":"message_send","parts":[]} b {"action":"message_send","parts":[{"type":"text","text":"the text one"}]}';
    expect(extractEnvelopeParts(raw)).toEqual([{ type: "text", text: "the text one" }]);
  });

  // GAP 2 — a non-message_send action (tool-call) in prose is NOT an envelope.
  it("returns null for prose embedding a non-message_send action object", () => {
    expect(
      extractEnvelopeParts('talking about {"action":"navigate","parts":["home"]} in my config'),
    ).toBeNull();
    expect(extractEnvelopeParts('{"action":"navigate","parts":["home"]}')).toBeNull();
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
              description: "",
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
