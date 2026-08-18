// @vitest-environment jsdom

import { describe, it, expect } from "vitest";
import { render } from "@testing-library/react";
import { ConversationUnreadAffordance } from "./conversation-muted";

/**
 * LRM-767 (design gate locked, Slack-aligned): an active conversation's plain
 * unread shows the REAL read-model-backed count in a neutral pill; a muted
 * conversation shows nothing (bold name is its only signal); a manual "mark
 * as unread" stays a dot (no honest count exists); the @-mention pill keeps
 * its existing semantics (pierces mute, brand accent).
 */
describe("ConversationUnreadAffordance", () => {
  it("renders a plain (non-@) unread as a numeric badge with the real count (LRM-767)", () => {
    const { container, getByText } = render(
      <ConversationUnreadAffordance
        realUnread={7}
        isManualDot={false}
        isMuted={false}
        unreadLabel="7 unread messages"
      />,
    );
    const badge = getByText("7");
    // Subtle outlined pill — not the brand/destructive accent reserved for @-mentions.
    expect(badge).toHaveClass("bg-background");
    expect(badge).toHaveClass("border-border/70");
    expect(badge).toHaveClass("text-[11px]");
    expect(badge).not.toHaveClass("bg-brand-solid");
    expect(badge).not.toHaveClass("bg-destructive");
    expect(badge).toHaveAttribute("aria-label", "7 unread messages");
    expect(container.querySelector(".size-2")).toBeNull();
  });

  it("caps the numeric badge at 99+", () => {
    const { getByText } = render(
      <ConversationUnreadAffordance realUnread={150} isManualDot={false} isMuted={false} />,
    );
    expect(getByText("99+")).toBeTruthy();
  });

  it("renders NOTHING for a muted plain-unread — bold name is the only signal (LRM-767)", () => {
    const { container } = render(
      <ConversationUnreadAffordance realUnread={150} isManualDot={false} isMuted />,
    );
    expect(container.querySelector("span")).toBeNull();
    expect(container).not.toHaveTextContent("99+");
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
    expect(pill).toHaveClass("bg-brand-solid");
    expect(pill).toHaveClass("text-brand-solid-foreground");
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

  it("shows the @N pill for muted conversations — @ pierces mute (Parker)", () => {
    const { getByText, container } = render(
      <ConversationUnreadAffordance
        realUnread={3}
        isManualDot={false}
        isMuted
        mentionCount={2}
        mentionLabel="You were mentioned"
      />,
    );
    // A direct @ surfaces the pill even when muted — mute silences ambient noise,
    // not direct mentions. The ambient unread (3) is NOT shown alongside the pill.
    const pill = getByText("@2");
    expect(pill).toHaveClass("bg-brand-solid");
    expect(pill).toHaveClass("text-brand-solid-foreground");
    expect(pill).not.toHaveClass("bg-destructive");
    expect(container).not.toHaveTextContent("3");
  });

  it("renders a no-@ unread row as a neutral numeric badge, never an @N pill (Iris regression guard)", () => {
    const { container, getByText } = render(
      <ConversationUnreadAffordance
        realUnread={4}
        isManualDot={false}
        isMuted={false}
        mentionCount={0}
      />,
    );
    expect(getByText("4")).toBeTruthy();
    expect(container.querySelector(".bg-brand-solid")).toBeNull();
    expect(container.querySelector(".bg-destructive")).toBeNull();
  });

  it("renders nothing when there is neither real unread nor a manual dot", () => {
    const { container } = render(
      <ConversationUnreadAffordance realUnread={0} isManualDot={false} isMuted={false} />,
    );
    expect(container.querySelector("span")).toBeNull();
  });
});
