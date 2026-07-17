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

  it("renders a muted plain-unread as a DIMMER dot with no count (Parker: muted is quietest, never a number)", () => {
    const { container } = render(
      <ConversationUnreadAffordance realUnread={150} isManualDot={false} isMuted />,
    );
    const dot = container.querySelector("span");
    expect(dot).toHaveClass("size-2");
    expect(dot).toHaveClass("bg-muted-foreground/50");
    // No surviving number — a muted row must never be more salient than an active one.
    expect(dot?.textContent ?? "").toBe("");
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

  it("keeps a muted unread dot DIMMER than an active one (no asymmetry — muted never louder)", () => {
    const active = render(
      <ConversationUnreadAffordance realUnread={3} isManualDot={false} isMuted={false} />,
    );
    const muted = render(
      <ConversationUnreadAffordance realUnread={3} isManualDot={false} isMuted />,
    );
    expect(active.container.querySelector("span")).toHaveClass("bg-muted-foreground");
    expect(muted.container.querySelector("span")).toHaveClass("bg-muted-foreground/50");
    // Neither shows a numeric count — the unread signal is the bold name in the row.
    expect(active.container).not.toHaveTextContent("3");
    expect(muted.container).not.toHaveTextContent("3");
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
    expect(pill).toHaveClass("bg-brand");
    expect(pill).not.toHaveClass("bg-destructive");
    expect(container).not.toHaveTextContent("3");
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
