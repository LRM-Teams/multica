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
  it("renders the real unread count (A4 — not a constant)", () => {
    const { container } = render(
      <ConversationUnreadAffordance realUnread={7} isManualDot={false} isMuted={false} />,
    );
    const badge = container.querySelector("span");
    expect(badge).not.toBeNull();
    expect(badge).toHaveTextContent("7");
    // Salient (unmuted) unread is the primary badge, never dimmed.
    expect(badge).toHaveClass("bg-primary");
  });

  it("caps large counts at 99+", () => {
    const { container } = render(
      <ConversationUnreadAffordance realUnread={150} isManualDot={false} isMuted={false} />,
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

  it("renders nothing when there is neither real unread nor a manual dot", () => {
    const { container } = render(
      <ConversationUnreadAffordance realUnread={0} isManualDot={false} isMuted={false} />,
    );
    expect(container.querySelector("span")).toBeNull();
  });
});
