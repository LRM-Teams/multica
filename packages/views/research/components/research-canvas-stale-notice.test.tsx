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

  it("prevents duplicate retries while recovery is pending", () => {
    render(<ResearchCanvasStaleNotice onRetry={() => {}} retryPending />);
    expect(screen.getByRole("button", { name: "Retrying…" })).toBeDisabled();
  });
});
