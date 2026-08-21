import { describe, expect, it } from "vitest";
import {
  CHAT_WINDOW_SIDEBAR_DEFAULT_WIDTH,
  CHAT_WINDOW_SIDEBAR_MAX_WIDTH,
  CHAT_WINDOW_SIDEBAR_MIN_WIDTH,
  chatWindowClosesOnOutsideClick,
  chatWindowShellClassName,
  chatWindowSidebarClipClassName,
  chatWindowSidebarSlideClassName,
  chatWindowUsesFloatingChrome,
  clampChatWindowSidebarWidth,
  noteAssistantSidebarPresence,
} from "./chat-window-layout";

describe("chat window layout chrome", () => {
  it("makes the sidebar a full-height right rail, not a corner card", () => {
    const sidebar = chatWindowShellClassName("sidebar");
    expect(sidebar).toContain("inset-y-0");
    expect(sidebar).toContain("right-0");
    expect(sidebar).toContain("border-l");
    expect(sidebar).toContain("absolute");
    expect(sidebar).not.toContain("fixed");
    expect(sidebar).not.toContain("bottom-2");
    expect(sidebar).not.toContain("rounded-xl");
    expect(sidebar).not.toContain("w-[min(24rem,100%)]");
  });

  it("omits the rail on first paint so a closed refresh cannot peek", () => {
    expect(noteAssistantSidebarPresence(false, false)).toBe("omit");
    expect(noteAssistantSidebarPresence(true, false)).toBe("open");
    expect(noteAssistantSidebarPresence(false, true)).toBe("closed");
  });

  it("clips the closed sidebar so it cannot reserve a blank page gutter", () => {
    const clip = chatWindowSidebarClipClassName();
    expect(clip).toContain("fixed");
    expect(clip).toContain("inset-0");
    expect(clip).toContain("overflow-hidden");
    expect(chatWindowSidebarSlideClassName(false)).toContain("translate-x-full");
    expect(chatWindowSidebarSlideClassName(true)).toContain("translate-x-0");
  });

  it("keeps floating chat as a bottom-right card", () => {
    const floating = chatWindowShellClassName("floating");
    expect(floating).toContain("bottom-2");
    expect(floating).toContain("right-2");
    expect(floating).toContain("rounded-xl");
  });

  it("does not treat the sidebar as a dismiss-on-outside-click bubble", () => {
    expect(chatWindowClosesOnOutsideClick("sidebar")).toBe(false);
    expect(chatWindowClosesOnOutsideClick("floating")).toBe(true);
    expect(chatWindowUsesFloatingChrome("sidebar")).toBe(false);
    expect(chatWindowUsesFloatingChrome("floating")).toBe(true);
  });

  it("clamps a dragged sidebar width to the note-rail range", () => {
    expect(clampChatWindowSidebarWidth(100)).toBe(CHAT_WINDOW_SIDEBAR_MIN_WIDTH);
    expect(clampChatWindowSidebarWidth(384)).toBe(CHAT_WINDOW_SIDEBAR_DEFAULT_WIDTH);
    expect(clampChatWindowSidebarWidth(800)).toBe(CHAT_WINDOW_SIDEBAR_MAX_WIDTH);
    expect(clampChatWindowSidebarWidth(Number.NaN)).toBe(CHAT_WINDOW_SIDEBAR_DEFAULT_WIDTH);
  });
});
