import { fireEvent, render, screen } from "@testing-library/react";
import { describe, expect, it, vi } from "vitest";
import { ResearchCanvasStaleNotice } from "./research-canvas-stale-notice";

vi.mock("../../i18n/use-t", () => ({
  useT: () => ({
    t: (selector: (bundle: Record<string, unknown>) => string) =>
      selector({
        d5: {
          canvas: {
            stale_title: "Canvas may be stale.",
            stale_body: "The last loaded constellation stays visible.",
          },
        },
        interrupt: { retrying: "Retrying…" },
        session_page: { retry: "Retry" },
      }),
  }),
}));

describe("ResearchCanvasStaleNotice", () => {
  it("keeps stale content recoverable with an alert and retry", () => {
    const onRetry = vi.fn();
    render(<ResearchCanvasStaleNotice onRetry={onRetry} />);

    expect(screen.getByRole("alert")).toHaveTextContent("Canvas may be stale.");
    fireEvent.click(screen.getByRole("button", { name: "Retry" }));
    expect(onRetry).toHaveBeenCalledTimes(1);
  });

  it("keeps focus and prevents duplicate retries while recovery is pending", () => {
    const onRetry = vi.fn();
    render(<ResearchCanvasStaleNotice onRetry={onRetry} retryPending />);
    const retry = screen.getByRole("button", { name: "Retrying…" });
    expect(retry).toHaveAttribute("aria-disabled", "true");
    expect(retry).toHaveAttribute("aria-busy", "true");
    expect(retry).not.toBeDisabled();
    retry.focus();
    fireEvent.click(retry);
    expect(retry).toHaveFocus();
    expect(onRetry).not.toHaveBeenCalled();
  });
});
