import { useRef } from "react";
import { act, render, screen } from "@testing-library/react";
import { afterEach, describe, expect, it, vi } from "vitest";
import { useSelectionQuoteMenu } from "./selection-quote-menu";

vi.mock("../../i18n/use-t", () => ({
  useT: () => ({ t: () => "Quote" }),
}));

vi.mock("../components/selection-quote-menu", () => ({
  SelectionQuoteMenu: ({
    resolved,
    onQuote,
  }: {
    resolved: unknown;
    onQuote: () => void;
  }) =>
    resolved ? (
      <button type="button" data-testid="selection-quote-menu" onClick={onQuote}>
        Quote
      </button>
    ) : null,
}));

function Harness({
  showMessages,
  onQuote = () => {},
}: {
  showMessages: boolean;
  onQuote?: Parameters<typeof useSelectionQuoteMenu>[0]["onQuote"];
}) {
  const containerRef = useRef<HTMLDivElement>(null);
  const selectionMenu = useSelectionQuoteMenu({
    containerRef,
    onQuote,
  });

  return (
    <>
      {showMessages ? (
        <div ref={containerRef}>
          <div data-message-author="Alice" data-message-id="message-1">
            <div data-testid="message-body">hello world</div>
          </div>
        </div>
      ) : null}
      {selectionMenu.menu}
    </>
  );
}

describe("useSelectionQuoteMenu", () => {
  afterEach(() => {
    vi.unstubAllGlobals();
  });

  it("starts handling selections when the message container mounts after the hook", () => {
    const onQuote = vi.fn();
    vi.stubGlobal(
      "matchMedia",
      () => ({ matches: false }) as MediaQueryList,
    );
    Object.defineProperty(window, "innerWidth", {
      configurable: true,
      value: 1200,
    });

    const { rerender } = render(<Harness showMessages={false} onQuote={onQuote} />);
    rerender(<Harness showMessages onQuote={onQuote} />);

    const body = screen.getByTestId("message-body");
    const range = document.createRange();
    range.selectNodeContents(body);
    Object.defineProperty(range, "getBoundingClientRect", {
      configurable: true,
      value: () => new DOMRect(20, 40, 80, 16),
    });
    vi.spyOn(window, "getSelection").mockReturnValue({
      isCollapsed: false,
      rangeCount: 1,
      toString: () => "hello world",
      getRangeAt: () => range,
      removeAllRanges: vi.fn(),
    } as unknown as Selection);

    act(() => document.dispatchEvent(new PointerEvent("pointerup")));

    expect(screen.getByTestId("selection-quote-menu")).toBeInTheDocument();
    act(() => screen.getByTestId("selection-quote-menu").click());
    expect(onQuote).toHaveBeenCalledWith({
      messageId: "message-1",
      author: "Alice",
      text: "hello world",
      rect: expect.any(DOMRect),
    });
  });
});
