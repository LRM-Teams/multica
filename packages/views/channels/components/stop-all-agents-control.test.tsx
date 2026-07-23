import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it, vi } from "vitest";
import { I18nProvider } from "@multica/core/i18n/react";
import { TooltipProvider } from "@multica/ui/components/ui/tooltip";
import enCommon from "../../locales/en/common.json";
import enChannels from "../../locales/en/channels.json";
import {
  StopAllAgentsHeaderButton,
  StopAllAgentsMenuItem,
} from "./stop-all-agents-control";

function renderUi(ui: React.ReactElement) {
  return render(
    <I18nProvider locale="en" resources={{ en: { common: enCommon, channels: enChannels } }}>
      <TooltipProvider delay={0}>{ui}</TooltipProvider>
    </I18nProvider>,
  );
}

describe("StopAllAgentsHeaderButton (LRM-405)", () => {
  it("opens confirm when agents are running", async () => {
    const onOpenConfirm = vi.fn();
    renderUi(
      <StopAllAgentsHeaderButton hasRunning onOpenConfirm={onOpenConfirm} />,
    );
    await userEvent.click(screen.getByTestId("stop-all-agents-header"));
    expect(onOpenConfirm).toHaveBeenCalledOnce();
  });

  it("stays disabled with empty-state aria when idle", () => {
    renderUi(
      <StopAllAgentsHeaderButton hasRunning={false} onOpenConfirm={vi.fn()} />,
    );
    const btn = screen.getByTestId("stop-all-agents-header");
    expect(btn).toBeDisabled();
    expect(btn).toHaveAttribute("aria-label", "No agents currently running");
  });

  it("does not open confirm while stopping", async () => {
    const onOpenConfirm = vi.fn();
    renderUi(
      <StopAllAgentsHeaderButton hasRunning stopping onOpenConfirm={onOpenConfirm} />,
    );
    await userEvent.click(screen.getByTestId("stop-all-agents-header"));
    expect(onOpenConfirm).not.toHaveBeenCalled();
  });
});

describe("StopAllAgentsMenuItem (LRM-405 mobile)", () => {
  it("shows empty hint and does not fire when idle", async () => {
    const onOpenConfirm = vi.fn();
    renderUi(
      <StopAllAgentsMenuItem hasRunning={false} onOpenConfirm={onOpenConfirm} />,
    );
    expect(screen.getByText("No agents currently running")).toBeInTheDocument();
    await userEvent.click(screen.getByTestId("stop-all-agents-menu"));
    expect(onOpenConfirm).not.toHaveBeenCalled();
  });

  it("opens confirm when agents are running", async () => {
    const onOpenConfirm = vi.fn();
    renderUi(
      <StopAllAgentsMenuItem hasRunning onOpenConfirm={onOpenConfirm} />,
    );
    await userEvent.click(screen.getByTestId("stop-all-agents-menu"));
    expect(onOpenConfirm).toHaveBeenCalledOnce();
  });
});
