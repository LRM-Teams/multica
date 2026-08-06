// @vitest-environment node
import { describe, expect, it } from "vitest";
import {
  CONVERSATION_SIDEBAR_ROW_ACTIVE,
  CONVERSATION_SIDEBAR_ROW_IDLE,
  CONVERSATION_SIDEBAR_UNREAD_BADGE,
} from "./conversation-sidebar-styles";

/**
 * LRM-354 — class contracts for Messages sidebar light/dark contrast.
 * Keeps selected / unread on semantic tokens (no primary opacity wash, no
 * primary fill for unread counts).
 */
describe("conversation-sidebar-styles (LRM-354)", () => {
  it("selected row uses sidebar-accent (light→surface, dark→subtle lift)", () => {
    expect(CONVERSATION_SIDEBAR_ROW_ACTIVE).toBe("bg-sidebar-accent");
    expect(CONVERSATION_SIDEBAR_ROW_ACTIVE).not.toMatch(/primary/);
    expect(CONVERSATION_SIDEBAR_ROW_ACTIVE).not.toMatch(/#|rgb|oklch/);
  });

  it("idle hover uses sidebar-accent (readable on bg-sidebar chrome)", () => {
    expect(CONVERSATION_SIDEBAR_ROW_IDLE).toBe("hover:bg-sidebar-accent");
    expect(CONVERSATION_SIDEBAR_ROW_IDLE).not.toBe("hover:bg-accent");
  });

  it("collapsed unread badge uses brand-solid + solid-foreground (≥4.5:1 on dark)", () => {
    expect(CONVERSATION_SIDEBAR_UNREAD_BADGE).toContain("bg-brand-solid");
    expect(CONVERSATION_SIDEBAR_UNREAD_BADGE).toContain("text-brand-solid-foreground");
    expect(CONVERSATION_SIDEBAR_UNREAD_BADGE).not.toContain("bg-primary");
  });
});
