/**
 * @vitest-environment happy-dom
 */
import { screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it, vi } from "vitest";
import { renderWithI18n } from "../test/i18n";
import { NotesCollectorSetupCard } from "./notes-collector-setup-card";

describe("NotesCollectorSetupCard", () => {
  it("names the Computer and lets the user configure or ignore", async () => {
    const onOpenRuntimePicker = vi.fn();
    const onDismiss = vi.fn();
    const user = userEvent.setup();
    renderWithI18n(
      <NotesCollectorSetupCard
        slotKey="local:pc-a"
        label="Laptop A"
        onOpenRuntimePicker={onOpenRuntimePicker}
        onDismiss={onDismiss}
      />,
      { locale: "zh-Hans" },
    );

    const card = screen.getByTestId("period-brief-collector-missing-local:pc-a");
    expect(card).toHaveAttribute("data-computer-label", "Laptop A");
    expect(card.textContent).toContain("Laptop A");
    await user.click(screen.getByTestId("period-brief-collector-missing-configure"));
    expect(onOpenRuntimePicker).toHaveBeenCalledTimes(1);
    await user.click(screen.getByTestId("period-brief-collector-missing-dismiss"));
    expect(onDismiss).toHaveBeenCalledTimes(1);
  });

  it("can reopen collect-root setup without creating a collector", async () => {
    const onOpenCollectRoots = vi.fn();
    const user = userEvent.setup();
    renderWithI18n(
      <NotesCollectorSetupCard
        slotKey="local:pc-a"
        label="Laptop A"
        onOpenCollectRoots={onOpenCollectRoots}
        onDismiss={vi.fn()}
      />,
      { locale: "zh-Hans" },
    );
    await user.click(screen.getByTestId("period-brief-collector-collect-roots"));
    expect(onOpenCollectRoots).toHaveBeenCalledTimes(1);
  });
});
