// @vitest-environment jsdom

import { screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it, vi } from "vitest";
import { renderWithI18n } from "../../test/i18n";
import { ComputerUpdateToast } from "./computer-update-toast";

describe("ComputerUpdateToast", () => {
  it("renders prompt actions and fires update/later", async () => {
    const user = userEvent.setup();
    const onUpdate = vi.fn();
    const onLater = vi.fn();
    renderWithI18n(
      <ComputerUpdateToast
        phase="prompt"
        title="Update available for MacBook"
        versionLine="0.3.0 → 0.4.0"
        updateLabel="Update now"
        laterLabel="Later"
        retryLabel="Retry"
        dismissLabel="Dismiss"
        onUpdate={onUpdate}
        onLater={onLater}
      />,
    );

    expect(screen.getByTestId("computer-update-toast")).toHaveAttribute(
      "data-phase",
      "prompt",
    );
    expect(screen.getByText("0.3.0 → 0.4.0")).toBeInTheDocument();
    await user.click(screen.getByRole("button", { name: "Update now" }));
    expect(onUpdate).toHaveBeenCalledOnce();
    await user.click(screen.getByRole("button", { name: "Later" }));
    expect(onLater).toHaveBeenCalledOnce();
  });

  it("shows updating spinner without primary actions", () => {
    renderWithI18n(
      <ComputerUpdateToast
        phase="updating"
        title="Updating MacBook…"
        progressLabel="Downloading…"
        updateLabel="Update now"
        laterLabel="Later"
        retryLabel="Retry"
        dismissLabel="Dismiss"
      />,
    );
    expect(screen.getByTestId("computer-update-toast")).toHaveAttribute(
      "data-phase",
      "updating",
    );
    expect(screen.queryByRole("button", { name: "Update now" })).toBeNull();
    expect(screen.getByText("Downloading…")).toBeInTheDocument();
  });

  it("shows retry on failed phase", async () => {
    const user = userEvent.setup();
    const onRetry = vi.fn();
    renderWithI18n(
      <ComputerUpdateToast
        phase="failed"
        title="Update failed for MacBook"
        errorLabel="network error"
        updateLabel="Update now"
        laterLabel="Later"
        retryLabel="Retry"
        dismissLabel="Dismiss"
        onRetry={onRetry}
        onLater={vi.fn()}
      />,
    );
    await user.click(screen.getByRole("button", { name: "Retry" }));
    expect(onRetry).toHaveBeenCalledOnce();
  });
});
