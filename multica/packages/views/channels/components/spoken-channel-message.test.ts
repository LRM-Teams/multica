import { describe, expect, it } from "vitest";
import {
  filterSpokenChannelMessages,
  isSpokenChannelMessageType,
  spokenMessagePreviewText,
} from "./spoken-channel-message";

describe("spoken-channel-message (LRM-873)", () => {
  it("keeps user and agent, drops system", () => {
    expect(isSpokenChannelMessageType("user")).toBe(true);
    expect(isSpokenChannelMessageType("agent")).toBe(true);
    expect(isSpokenChannelMessageType("system")).toBe(false);
    expect(isSpokenChannelMessageType("lark")).toBe(false);

    const out = filterSpokenChannelMessages([
      { type: "user" },
      { type: "system" },
      { type: "agent" },
      { type: "system" },
    ]);
    expect(out.map((m) => m.type)).toEqual(["user", "agent"]);
  });

  it("builds preview placeholders for media-only messages", () => {
    expect(
      spokenMessagePreviewText(
        { content: "  hello  world  ", parts: [], attachments: [] },
        { image: "[Image]", sticker: "[Sticker]", attachment: "[Attachment]" },
      ),
    ).toBe("hello world");

    expect(
      spokenMessagePreviewText(
        { content: "", parts: [{ type: "sticker" } as never], attachments: [] },
        { image: "[Image]", sticker: "[Sticker]", attachment: "[Attachment]" },
      ),
    ).toBe("[Sticker]");
  });
});
