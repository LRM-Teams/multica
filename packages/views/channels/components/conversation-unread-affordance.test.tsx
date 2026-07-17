// @vitest-environment jsdom

import { describe, it, expect } from "vitest";
import { render } from "@testing-library/react";
import { ConversationUnreadAffordance } from "./conversation-muted";

/**
 * Anchor 7 (A4 / A6) + the Parker "no visible fake count" gate. The unread
 * affordance must render the read-model `real_unread`-backed REAL count — never
 * a constant / fabricated number — and must present muted conversations
 * silently (dimmed, not the salient primary/red badge).
 */
describe("ConversationUnreadAffordance", () => {
  it("renders a plain (non-@) unread as a subtle neutral dot, not a saturated count (#3 Slack-style)", () => {
    const { container } = render(
      <ConversationUnreadAffordance realUnread={7} isManualDot={false} isMuted={false} />,
    );
    const dot = container.querySelector("span");
    expect(dot).not.toBeNull();
    // No saturated count block — the unread signal is the bold channel name in
    // the row; the numeric block is reserved for the @-mention pill.
    expect(dot).toHaveClass("size-2");
    expect(dot).toHaveClass("bg-muted-foreground");
    expect(dot).not.toHaveClass("bg-primary");
    expect(dot?.textContent ?? "").toBe("");
  });

  it("caps large muted counts at 99+ (the dimmed count is the only surviving number)", () => {
    const { container } = render(
      <ConversationUnreadAffordance realUnread={150} isManualDot={false} isMuted />,
    );
    expect(container.querySelector("span")).toHaveTextContent("99+");
  });

  it("renders a manual-unread DOT, never a fabricated '1' (Parker gate)", () => {
    // Manual unread: the server bumps `unread` to >= 1 but `real_unread` stays 0.
    // The row must show a marker dot, NOT a fake numeric badge — otherwise the
    // count would be a visible lie about how many messages are actually unread.
    const { container } = render(
      <ConversationUnreadAffordance realUnread={0} isManualDot isMuted={false} />,
    );
    const dot = container.querySelector("span");
    expect(dot).not.toBeNull();
    expect(dot).toHaveClass("size-2");
    expect(dot).toHaveClass("rounded-full");
    // No digits: the manual dot is a marker, not a count.
    expect(dot?.textContent ?? "").toBe("");
  });

  it("dims the count for muted conversations — silent, no primary/red badge (A6)", () => {
    const { container } = render(
      <ConversationUnreadAffordance realUnread={3} isManualDot={false} isMuted />,
    );
    const badge = container.querySelector("span");
    expect(badge).toHaveTextContent("3");
    expect(badge).toHaveClass("bg-muted-foreground/25");
    expect(badge).not.toHaveClass("bg-primary");
  });

  it("dims the manual dot for muted conversations (A6)", () => {
    const { container } = render(
      <ConversationUnreadAffordance realUnread={0} isManualDot isMuted />,
    );
    const dot = container.querySelector("span");
    expect(dot).toHaveClass("bg-muted-foreground/50");
    expect(dot).not.toHaveClass("bg-primary");
  });

  it("shows a single @N pill (glyph + colour), replacing the plain count (#556 — no stacked pills)", () => {
    const { getByText, container } = render(
      <ConversationUnreadAffordance
        realUnread={5}
        isManualDot={false}
        isMuted={false}
        mentionCount={2}
        mentionLabel="You were mentioned"
        mentionTooltip="5 unread · 2 @ you"
      />,
    );
    // The `@` glyph is the primary, colour-blind-safe cue (A6); emphasis colour secondary.
    const pill = getByText("@2");
    // Mention accent is brand blue now (red is reserved for errors only, #1).
    expect(pill).toHaveClass("bg-brand");
    expect(pill).not.toHaveClass("bg-destructive");
    expect(pill).toHaveAttribute("aria-label", "You were mentioned");
    // Only @N shows — the plain unread count (5) is NOT stacked alongside it.
    expect(container).not.toHaveTextContent("5");
  });

  it("caps the @N pill at @99+", () => {
    const { getByText } = render(
      <ConversationUnreadAffordance
        realUnread={200}
        isManualDot={false}
        isMuted={false}
        mentionCount={150}
        mentionLabel="You were mentioned"
      />,
    );
    expect(getByText("@99+")).toBeTruthy();
  });

  it("does not show the @N pill for muted conversations — falls back to the dimmed count (muted @-pierce is follow-up)", () => {
    const { container } = render(
      <ConversationUnreadAffordance
        realUnread={3}
        isManualDot={false}
        isMuted
        mentionCount={2}
        mentionLabel="You were mentioned"
      />,
    );
    expect(container.querySelector(".bg-destructive")).toBeNull();
    // The dimmed unread count still renders so the conversation isn't lost.
    expect(container).toHaveTextContent("3");
    expect(container.querySelector(".bg-muted-foreground\\/25")).not.toBeNull();
  });

  it("renders a no-@ unread row as a neutral dot, never an @N pill (Iris regression guard)", () => {
    const { container } = render(
      <ConversationUnreadAffordance
        realUnread={4}
        isManualDot={false}
        isMuted={false}
        mentionCount={0}
      />,
    );
    const dot = container.querySelector("span");
    // Plain unread is a subtle neutral dot — no saturated count, no @N pill.
    expect(dot).toHaveClass("size-2");
    expect(dot).toHaveClass("bg-muted-foreground");
    expect(dot).not.toHaveClass("bg-primary");
    expect(container.querySelector(".bg-brand")).toBeNull();
    expect(container.querySelector(".bg-destructive")).toBeNull();
  });

  it("renders nothing when there is neither real unread nor a manual dot", () => {
    const { container } = render(
      <ConversationUnreadAffordance realUnread={0} isManualDot={false} isMuted={false} />,
    );
    expect(container.querySelector("span")).toBeNull();
  });
});
