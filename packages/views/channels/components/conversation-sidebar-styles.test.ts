// @vitest-environment node
import { describe, expect, it } from "vitest";
import {
  CONVERSATION_LIST_PANE_BG,
  CONVERSATION_SIDEBAR_ROW_ACTIVE,
  CONVERSATION_SIDEBAR_ROW_IDLE,
  CONVERSATION_SIDEBAR_UNREAD_BADGE,
} from "./conversation-sidebar-styles";

/**
 * Conversation list contrast: list pane is the product surface so it does
 * not merge with app-sidebar chrome. Selected / unread stay on semantic
 * tokens (no primary opacity wash, no primary fill for unread counts).
 */
describe("conversation-sidebar-styles", () => {
  it("list pane uses background, not sidebar chrome", () => {
    expect(CONVERSATION_LIST_PANE_BG).toBe("bg-background");
    expect(CONVERSATION_LIST_PANE_BG).not.toMatch(/sidebar/);
  });

  it("selected row uses muted (visible on the surface list plane)", () => {
    expect(CONVERSATION_SIDEBAR_ROW_ACTIVE).toBe("bg-muted");
    expect(CONVERSATION_SIDEBAR_ROW_ACTIVE).not.toMatch(/primary/);
    expect(CONVERSATION_SIDEBAR_ROW_ACTIVE).not.toMatch(/#|rgb|oklch/);
  });

  it("idle hover uses accent (frozen --hover on surface)", () => {
    expect(CONVERSATION_SIDEBAR_ROW_IDLE).toBe("hover:bg-accent");
    expect(CONVERSATION_SIDEBAR_ROW_IDLE).not.toMatch(/sidebar/);
  });

  it("collapsed unread badge uses brand-solid + solid-foreground (≥4.5:1 on dark)", () => {
    expect(CONVERSATION_SIDEBAR_UNREAD_BADGE).toContain("bg-brand-solid");
    expect(CONVERSATION_SIDEBAR_UNREAD_BADGE).toContain("text-brand-solid-foreground");
    expect(CONVERSATION_SIDEBAR_UNREAD_BADGE).not.toContain("bg-primary");
  });
});
