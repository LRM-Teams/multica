import { describe, expect, it, vi, afterEach } from "vitest";
import { render, screen, fireEvent } from "@testing-library/react";
import { Markdown } from "@multica/ui/markdown";

/**
 * #836 — a link in message content must keep working when nothing takes it over.
 *
 * The reported bug: clicking an attachment link in a message did nothing. No
 * download, no preview, no error. Verified on served (`leagent.me`) against the
 * original fixture: the handler ran, it received the correct href, and the
 * server answered 200 with `Content-Disposition: attachment` — the link and the
 * backend were both fine. What broke it was our own click handler, which called
 * `preventDefault()` unconditionally and then re-implemented the anchor with
 * `window.open(href, '_blank', 'noopener,noreferrer')`.
 *
 * That substitute is strictly worse than the anchor it replaced:
 *   - it needs popup permission, so anything can block it;
 *   - with `noopener` it returns null whether or not it was blocked, so neither
 *     the app nor an investigator can tell success from failure;
 *   - it reports nothing either way.
 * A blocked popup and a silent successful download are therefore identical from
 * the outside — which is exactly why this bug survived several rounds.
 *
 * No view in the repo passes `onUrlClick` (Felix, `git grep "onUrlClick="` → 0
 * hits), so EVERY link in message content took that branch.
 *
 * HOW TO FLIP-VERIFY: put `e.preventDefault()` back at the top of `handleClick`
 * in `packages/ui/markdown/Markdown.tsx` → the first case goes red. Restore the
 * `window.open` fallback → the second goes red.
 */
describe("Markdown links — the browser's own behaviour is left alone (#836)", () => {
  afterEach(() => {
    vi.restoreAllMocks();
  });

  const ATTACHMENT = "/api/attachments/019fa86b-0eef-7ded-9717-c4e311f993b8/download";

  it("does NOT cancel the click when no handler is taking over", () => {
    render(<Markdown>{`[fixture](${ATTACHMENT})`}</Markdown>);
    const anchor = screen.getByRole("link", { name: "fixture" });

    // fireEvent.click returns false when the event was canceled.
    const notCanceled = fireEvent.click(anchor);

    expect(notCanceled).toBe(true);
    expect(anchor).toHaveAttribute("href", ATTACHMENT);
  });

  it("does NOT reach for window.open — that is the mechanism that failed silently", () => {
    const open = vi.spyOn(window, "open").mockReturnValue(null);
    render(<Markdown>{`[fixture](${ATTACHMENT})`}</Markdown>);

    fireEvent.click(screen.getByRole("link", { name: "fixture" }));

    expect(open).not.toHaveBeenCalled();
  });

  it("still opens in a new tab, via the anchor rather than a popup", () => {
    render(<Markdown>{`[fixture](${ATTACHMENT})`}</Markdown>);
    const anchor = screen.getByRole("link", { name: "fixture" });

    // A user-initiated target=_blank navigation is not a popup, so no permission
    // is involved. On desktop Electron routes it through the same
    // setWindowOpenHandler → openExternalSafely path it already used for
    // window.open, so external links behave as before (main/index.ts:200).
    expect(anchor).toHaveAttribute("target", "_blank");
    expect(anchor).toHaveAttribute("rel", "noopener noreferrer");
  });

  it("STILL cancels the click when a handler genuinely takes over", () => {
    // The fix must not turn `onUrlClick` into a no-op that also navigates: when
    // a consumer handles the URL, the browser must not ALSO follow the link.
    const onUrlClick = vi.fn();
    render(<Markdown onUrlClick={onUrlClick}>{`[fixture](${ATTACHMENT})`}</Markdown>);

    const notCanceled = fireEvent.click(screen.getByRole("link", { name: "fixture" }));

    expect(onUrlClick).toHaveBeenCalledWith(ATTACHMENT);
    expect(notCanceled).toBe(false);
  });
});
