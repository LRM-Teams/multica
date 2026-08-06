import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it, vi } from "vitest";
import { I18nProvider } from "@multica/core/i18n/react";
import enCommon from "../../locales/en/common.json";
import enChannels from "../../locales/en/channels.json";
import { StopAllAgentsDialog } from "./stop-all-agents-dialog";

function renderDialog(
  ui: React.ReactElement,
) {
  return render(
    <I18nProvider locale="en" resources={{ en: { common: enCommon, channels: enChannels } }}>
      {ui}
    </I18nProvider>,
  );
}

describe("StopAllAgentsDialog (LRM-405 / LRM-447)", () => {
  it("shows Frank's confirm copy with the channel name and does not confirm on cancel/close", async () => {
    const onConfirm = vi.fn();
    const onOpenChange = vi.fn();
    renderDialog(
      <StopAllAgentsDialog
        open
        onOpenChange={onOpenChange}
        channelName="ai-research"
        onConfirm={onConfirm}
      />,
    );

    expect(screen.getByTestId("stop-all-agents-dialog")).toBeInTheDocument();
    expect(screen.getByRole("heading", { name: /stop all agents/i })).toBeInTheDocument();
    const warning = screen.getByRole("alert");
    expect(warning).toHaveTextContent(/immediately stop all running agents/i);
    expect(warning).toHaveTextContent("#ai-research");
    expect(warning.querySelector("strong")).toHaveTextContent("#ai-research");
    // LRM-447 design A — token destructive wash, not hardcoded cream/coral.
    expect(warning.className).toMatch(/destructive/);
    expect(warning.className).not.toMatch(/#FCE8E4|#E8916A/);

    await userEvent.click(screen.getByRole("button", { name: /^cancel$/i }));
    expect(onOpenChange).toHaveBeenCalledWith(false);
    expect(onConfirm).not.toHaveBeenCalled();
  });

  it("calls onConfirm only after the Stop All Agents action", async () => {
    const onConfirm = vi.fn();
    const onOpenChange = vi.fn();
    renderDialog(
      <StopAllAgentsDialog
        open
        onOpenChange={onOpenChange}
        channelName="#ops"
        onConfirm={onConfirm}
      />,
    );

    // Channel name already has a leading # — do not double it.
    const warning = screen.getByRole("alert");
    expect(warning).toHaveTextContent("#ops");
    expect(warning).not.toHaveTextContent("##ops");

    const confirm = screen.getByTestId("stop-all-agents-confirm");
    expect(confirm.className).not.toMatch(/shadow-\[2px_2px_0/);
    expect(confirm.className).toMatch(/destructive/);

    await userEvent.click(confirm);
    expect(onOpenChange).toHaveBeenCalledWith(false);
    expect(onConfirm).toHaveBeenCalledTimes(1);
  });
});
