import { render, act } from "@testing-library/react";
import { useRef } from "react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { useLinkHover } from "./link-hover-card";

vi.mock("@multica/core/paths", () => ({ useWorkspaceSlug: () => "acme" }));
vi.mock("../i18n", () => ({ useT: () => ({ t: (fn: unknown) => String(fn) }) }));
vi.mock("@multica/ui/lib/clipboard", () => ({ copyText: vi.fn() }));
vi.mock("sonner", () => ({ toast: { success: vi.fn() } }));
vi.mock("./utils/link-handler", () => ({
  openLink: vi.fn(),
  isMentionHref: (href: string) => href.startsWith("mention://"),
}));

/**
 * Drives the hook against a real container of anchors and reports whether the URL
 * hover card would show for the one we hover.
 */
function Harness({ html }: { html: string }) {
  const ref = useRef<HTMLDivElement>(null);
  const { visible, href } = useLinkHover(ref);
  return (
    <div>
      <div ref={ref} dangerouslySetInnerHTML={{ __html: html }} />
      <span data-testid="visible">{String(visible)}</span>
      <span data-testid="href">{href}</span>
    </div>
  );
}

function hover(container: HTMLElement, selector: string) {
  const link = container.querySelector(selector);
  if (!link) throw new Error(`no link for ${selector}`);
  act(() => {
    link.dispatchEvent(new MouseEvent("mouseover", { bubbles: true }));
  });
  // The card is delayed by SHOW_DELAY (300ms).
  act(() => {
    vi.advanceTimersByTime(400);
  });
}

describe("useLinkHover — links that own their hover must not get a second one", () => {
  beforeEach(() => vi.useFakeTimers());
  afterEach(() => vi.useRealTimers());

  it("shows the URL card for an ordinary link", () => {
    // The control: without this passing, the suppression tests below would pass
    // for the wrong reason (a hook that never shows anything suppresses everything).
    const { container, getByTestId } = render(
      <Harness html={`<a href="https://example.com/x" id="plain">x</a>`} />,
    );
    hover(container, "#plain");
    expect(getByTestId("visible")).toHaveTextContent("true");
  });

  it("stands down for an issue reference (data-issue-ref) — it has its own peek", () => {
    // Frank's report: hovering LRM-127 in an issue popped BOTH the peek card and the
    // URL preview. #520 turned the chip into a plain <a> and dropped the
    // `issue-mention` class as if it were styling — it also carried this suppression.
    // The marker is now an attribute that says what the link IS, so a restyle cannot
    // silently take the behaviour with it.
    const { container, getByTestId } = render(
      <Harness html={`<a href="/acme/issues/abc" data-issue-ref="" id="ref">LRM-127</a>`} />,
    );
    hover(container, "#ref");
    expect(getByTestId("visible")).toHaveTextContent("false");
  });

  it("still stands down for the editor's chip anchors (.issue-mention)", () => {
    // The editor's own operating-state chips keep the class-based path.
    const { container, getByTestId } = render(
      <Harness html={`<a href="/acme/issues/abc" class="issue-mention" id="chip">LRM-1</a>`} />,
    );
    hover(container, "#chip");
    expect(getByTestId("visible")).toHaveTextContent("false");
  });
});
