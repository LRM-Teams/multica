import { describe, expect, it } from "vitest";
import {
  CONVERSATION_LIST_ROW_IDLE_CLASS,
  CONVERSATION_LIST_ROW_SELECTED_CLASS,
  SIDEBAR_UNREAD_COUNT_BADGE_CLASS,
} from "./conversation-list-row-tokens";

describe("conversation-list-row-tokens (LRM-354)", () => {
  it("selected wash uses accent, never primary alpha (dark primary ≈ white wash)", () => {
    expect(CONVERSATION_LIST_ROW_SELECTED_CLASS).toBe("bg-accent");
    expect(CONVERSATION_LIST_ROW_SELECTED_CLASS).not.toMatch(/primary/);
    expect(CONVERSATION_LIST_ROW_IDLE_CLASS).toMatch(/hover:bg-accent/);
    expect(CONVERSATION_LIST_ROW_IDLE_CLASS).not.toMatch(/primary/);
  });

  it("collapsed / Activity unread badge is brand (not primary)", () => {
    expect(SIDEBAR_UNREAD_COUNT_BADGE_CLASS).toMatch(/bg-brand/);
    expect(SIDEBAR_UNREAD_COUNT_BADGE_CLASS).toMatch(/text-brand-foreground/);
    expect(SIDEBAR_UNREAD_COUNT_BADGE_CLASS).not.toMatch(/primary/);
  });
});
