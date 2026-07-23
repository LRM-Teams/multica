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
import { StopAllAgentsConfirmDialog } from "./stop-all-agents-dialog";
import { listStoppableChannelTasks } from "./conversation-activity-tasks";
import type { ChannelActiveTask } from "@multica/core/types";

function renderUi(ui: React.ReactElement) {
  return render(
    <I18nProvider locale="en" resources={{ en: { common: enCommon, channels: enChannels } }}>
      <TooltipProvider delay={0}>{ui}</TooltipProvider>
    </I18nProvider>,
  );
}

function task(over: Partial<ChannelActiveTask> = {}): ChannelActiveTask {
  return { agent_id: "a1", agent_name: "Aria", task_id: "t1", status: "running", ...over };
}

describe("listStoppableChannelTasks (LRM-405)", () => {
  it("keeps non-terminal reply tasks and drops terminals / issue_create", () => {
    expect(
      listStoppableChannelTasks([
        task({ task_id: "t1" }),
        task({ task_id: "t2", status: "failed", outcome: "failed" }),
        task({ task_id: "t3", kind: "issue_create" }),
      ]).map((row) => row.task_id),
    ).toEqual(["t1"]);
  });
});

describe("StopAllAgentsHeaderButton", () => {
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
    expect(btn).toHaveAttribute("aria-label", "No agents running");
  });
});

describe("StopAllAgentsMenuItem", () => {
  it("shows empty hint and does not fire when idle", async () => {
    const onOpenConfirm = vi.fn();
    renderUi(
      <StopAllAgentsMenuItem hasRunning={false} onOpenConfirm={onOpenConfirm} />,
    );
    expect(screen.getByText("No agents running")).toBeInTheDocument();
    await userEvent.click(screen.getByTestId("stop-all-agents-menu"));
    expect(onOpenConfirm).not.toHaveBeenCalled();
  });
});

describe("StopAllAgentsConfirmDialog", () => {
  it("renders Frank copy with channel name and only confirms on action", async () => {
    const onConfirm = vi.fn();
    const onOpenChange = vi.fn();
    renderUi(
      <StopAllAgentsConfirmDialog
        open
        onOpenChange={onOpenChange}
        channelName="ai-research"
        onConfirm={onConfirm}
      />,
    );
    expect(screen.getByTestId("stop-all-agents-dialog")).toBeInTheDocument();
    expect(
      screen.getByRole("heading", { name: /stop all agents/i }),
    ).toBeInTheDocument();
    expect(screen.getAllByText("#ai-research").length).toBeGreaterThan(0);
    expect(screen.getByTestId("stop-all-agents-confirm")).toHaveTextContent(
      "Stop All Agents",
    );

    await userEvent.click(screen.getByRole("button", { name: "Cancel" }));
    expect(onOpenChange).toHaveBeenCalledWith(false);
    expect(onConfirm).not.toHaveBeenCalled();

    onOpenChange.mockClear();
    await userEvent.click(screen.getByTestId("stop-all-agents-confirm"));
    expect(onConfirm).toHaveBeenCalledOnce();
  });
});
