import { describe, expect, it } from "vitest";
import {
  contentMentionsViewer,
  messageMentionsViewer,
} from "./mentions-viewer";

describe("contentMentionsViewer", () => {
  it("returns false for empty content or missing viewer", () => {
    expect(contentMentionsViewer("", "user-1")).toBe(false);
    expect(contentMentionsViewer(null, "user-1")).toBe(false);
    expect(contentMentionsViewer("hello", null)).toBe(false);
    expect(contentMentionsViewer("hello [@Alice](mention://member/user-1)", null)).toBe(
      false,
    );
  });

  it("detects a direct member mention of the viewer", () => {
    expect(
      contentMentionsViewer(
        "hey [@Alice](mention://member/user-1) look",
        "user-1",
      ),
    ).toBe(true);
  });

  it("ignores mentions of other members and agents", () => {
    expect(
      contentMentionsViewer(
        "hey [@Bob](mention://member/user-2) and [@Bot](mention://agent/user-1)",
        "user-1",
      ),
    ).toBe(false);
  });

  it("treats @all as addressing the viewer", () => {
    expect(contentMentionsViewer("[@all](mention://all/all) heads up", "user-1")).toBe(
      true,
    );
    expect(contentMentionsViewer("[@all](mention://all/all)", null)).toBe(true);
  });

  it("does not false-positive on display-name substrings", () => {
    expect(contentMentionsViewer("talk to @Alice later", "user-1")).toBe(false);
    expect(
      contentMentionsViewer("see mention://member/user-12 elsewhere", "user-1"),
    ).toBe(false);
  });

  it("normalizes legacy mention shortcodes", () => {
    expect(
      contentMentionsViewer(
        '[@ id="user-1" label="Alice"] please review',
        "user-1",
      ),
    ).toBe(true);
  });
});

describe("messageMentionsViewer", () => {
  it("falls through to structured text parts when content is plain", () => {
    expect(
      messageMentionsViewer("see parts", "user-1", [
        { type: "text", text: "ping [@Alice](mention://member/user-1)" },
      ]),
    ).toBe(true);
  });

  it("ignores sticker parts", () => {
    expect(
      messageMentionsViewer("plain", "user-1", [
        { type: "sticker", sticker_id: "s1", alt: "@Alice" },
      ]),
    ).toBe(false);
  });
});
