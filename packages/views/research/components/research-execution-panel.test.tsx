import { fireEvent, render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it, vi } from "vitest";
import {
  researchExecutionPanelFixture,
  type ResearchExecutionStatus,
} from "../lib/research-execution-panel-fixture";
import { ResearchExecutionPanel } from "./research-execution-panel";
vi.mock("../../i18n/use-t", () => ({
  useT: () => ({
    t: (fn: (dict: Record<string, unknown>) => unknown, values?: { location?: string }) =>
      String(fn({ panel: { execution: { title: "执行动态", locatable: "可定位至 {{location}}", locate: "定位至 {{location}}", unavailable: "暂无可定位节点", load_failed: "执行状态加载失败", retry: "重试", empty: "暂无执行成员" } } })).replace("{{location}}", values?.location ?? ""),
  }),
}));


const agents = researchExecutionPanelFixture.map((agent) =>
  agent.id === "running" || agent.id === "failed"
    ? { ...agent, currentNodeId: `node-${agent.id}` }
    : agent,
);

describe("ResearchExecutionPanel", () => {
  it("shows the mixed status roster", () => {
    render(<ResearchExecutionPanel agents={agents} />);
    expect(screen.getByText("1 个智能体执行中")).toBeTruthy();
    const statuses = new Set(
      screen.getAllByTestId("research-execution-row").map(
        (row) => row.getAttribute("data-status") as ResearchExecutionStatus,
      ),
    );
    expect(statuses).toEqual(new Set(["queued", "running", "done", "failed", "stale", "idle"]));
  });

  it("sends one locate request from click, Enter, or Space and keeps the last selection", async () => {
    const user = userEvent.setup();
    const onLocate = vi.fn();
    render(<ResearchExecutionPanel agents={agents} onLocate={onLocate} />);
    const running = screen.getByRole("button", { name: "定位Lin当前节点" });
    const failed = screen.getByRole("button", { name: "定位Owen当前节点" });

    fireEvent.click(running);
    failed.focus();
    await user.keyboard("{Enter}");
    running.focus();
    await user.keyboard(" ");

    expect(onLocate).toHaveBeenCalledTimes(3);
    expect(onLocate.mock.calls.map(([agent]) => agent.currentNodeId)).toEqual([
      "node-running",
      "node-failed",
      "node-running",
    ]);
    expect(document.activeElement).toBe(running);
  });

  it("expands recent activity and explains an unlocatable worker", () => {
    const onLocate = vi.fn();
    render(<ResearchExecutionPanel agents={agents} onLocate={onLocate} />);
    const idle = screen.getByRole("button", { name: "查看苏澄最近活动" });
    idle.focus();
    fireEvent.click(idle);
    expect(screen.getByText("当前没有可领取的小任务。")).toBeTruthy();
    expect(screen.getByText("暂无可定位节点")).toBeTruthy();
    expect(onLocate).not.toHaveBeenCalled();
    expect(document.activeElement).toBe(idle);
  });

  it("expands a locatable failed row to read failure detail while still locating once", async () => {
    const user = userEvent.setup();
    const onLocate = vi.fn();
    render(<ResearchExecutionPanel agents={agents} onLocate={onLocate} />);
    const failed = screen.getByRole("button", { name: "定位Owen当前节点" });
    expect(failed).toHaveAttribute("aria-expanded", "false");

    // 鼠标点击：既定位一次，又展开失败详情
    failed.focus();
    fireEvent.click(failed);
    expect(onLocate).toHaveBeenCalledTimes(1);
    expect(onLocate.mock.calls[0]?.[0].currentNodeId).toBe("node-failed");
    expect(screen.getByText("无法识别日期列格式。请清洗输入后重试。")).toBeTruthy();
    expect(failed).toHaveAttribute("aria-expanded", "true");
    expect(document.activeElement).toBe(failed);

    // 再点击：展开态收起，但不会再次触发定位
    fireEvent.click(failed);
    expect(onLocate).toHaveBeenCalledTimes(2);
    expect(screen.queryByText("无法识别日期列格式。请清洗输入后重试。")).toBeNull();
    expect(failed).toHaveAttribute("aria-expanded", "false");

    // 键盘 Enter 同样可展开并保持焦点
    failed.focus();
    await user.keyboard("{Enter}");
    expect(screen.getByText("无法识别日期列格式。请清洗输入后重试。")).toBeTruthy();
    expect(failed).toHaveAttribute("aria-expanded", "true");
    expect(document.activeElement).toBe(failed);
  });

  it("renders an empty roster and a focus-stable retryable error", () => {
    const retry = vi.fn();
    const { rerender } = render(<ResearchExecutionPanel agents={[]} />);
    expect(screen.getByText("暂无执行成员")).toBeTruthy();

    rerender(<ResearchExecutionPanel agents={[]} error="network" onRetry={retry} />);
    const button = screen.getByRole("button", { name: "重试" });
    button.focus();
    fireEvent.click(button);
    rerender(<ResearchExecutionPanel agents={[]} error="network" onRetry={retry} isRetrying />);
    expect(retry).toHaveBeenCalledTimes(1);
    expect(document.activeElement).toBe(button);
    expect(button).toHaveAttribute("aria-disabled", "true");
  });

  it("keeps reduced-motion and narrow-layout guards", () => {
    const { container } = render(<ResearchExecutionPanel agents={agents} />);
    const running = screen.getAllByTestId("research-execution-row").find((row) => row.dataset.status === "running");
    expect(running?.querySelector(".animate-nav-progress-sweep")?.className).toContain("motion-reduce:hidden");
    expect(container.firstElementChild?.className).toContain("min-w-0");
    expect(container.querySelector(".grid-cols-\\[auto_minmax\\(0\\,1fr\\)_auto\\]")).toBeTruthy();
  });
});
