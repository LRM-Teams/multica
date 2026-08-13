import { fireEvent, render, screen } from "@testing-library/react";
import { describe, expect, it, vi } from "vitest";
import { ResearchPendingRetryButton } from "./research-pending-retry-button";

describe("ResearchPendingRetryButton", () => {
  it("runs an idle retry", () => {
    const onRetry = vi.fn();
    render(
      <ResearchPendingRetryButton
        label="Retry"
        pendingLabel="Retrying…"
        onRetry={onRetry}
      />,
    );

    fireEvent.click(screen.getByRole("button", { name: "Retry" }));
    expect(onRetry).toHaveBeenCalledTimes(1);
  });

  it("stays focused but suppresses repeat activation while pending", () => {
    const onRetry = vi.fn();
    render(
      <ResearchPendingRetryButton
        label="Retry"
        pendingLabel="Retrying…"
        pending
        onRetry={onRetry}
      />,
    );

    const retry = screen.getByRole("button", {
      name: "Retrying…",
    }) as HTMLButtonElement;
    expect(retry.disabled).toBe(false);
    expect(retry).toHaveAttribute("aria-disabled", "true");
    expect(retry).toHaveAttribute("aria-busy", "true");
    retry.focus();
    fireEvent.click(retry);
    fireEvent.keyDown(retry, { key: "Enter" });
    expect(document.activeElement).toBe(retry);
    expect(onRetry).not.toHaveBeenCalled();
  });
});
