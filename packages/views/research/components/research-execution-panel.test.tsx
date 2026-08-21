import { fireEvent, render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it, vi } from "vitest";
import { researchExecutionPanelFixture } from "../lib/research-execution-panel-fixture";
import { ResearchExecutionPanel } from "./research-execution-panel";

// The row/inspector now render the site-wide smart avatar, which resolves
// identity through workspace queries. These suites are about execution copy.
vi.mock("../../common/actor-avatar", () => ({
  ActorAvatar: ({ actorId }: { actorId: string }) => (
    <span data-testid={`actor-avatar-${actorId}`} />
  ),
}));

// LRM-1479 — ResearchExecutionPanel now delegates to ExecutionOverlayPanel.
// The mock mirrors the overlay `panel.execution` bundle so chrome is fully
// translated; statuses are asserted by 8-state mapping.
vi.mock("../../i18n/use-t", () => ({
  useT: () => ({
    t: (fn: (dict: Record<string, unknown>) => unknown, values?: { location?: string; name?: string; count?: number; time?: string; anomaly?: number; running?: number; queued?: number; total?: number }) =>
      String(fn({ panel: { execution: {
        title: "执行动态",
        locatable: "可定位至 {{location}}",
        locate: "定位至 {{location}}",
        unavailable: "暂无可定位节点",
        load_failed: "执行状态加载失败",
        retry: "重试",
        empty: "暂无执行成员",
        active_count: "{{count}} 个智能体执行中",
        no_active: "暂无智能体执行",
        locate_aria: "定位{{name}}当前节点",
        view_aria: "查看{{name}}最近活动",
        expand_aria: "展开{{name}}详情",
        recent_result: "最近已验收产出",
        started: "开始",
        updated: "更新",
        duration: "已持续",
        stage: "阶段",
        wait_reason: "等待原因",
        stale_reason: "过期原因",
        task: "任务",
        attempt: "尝试",
        task_objective: "任务",
        collapse_counts: "{{anomaly}} 异常 · {{running}} 运行 · {{queued}} 排队 · {{total}} 人",
        collapsed_hint: "点击计数栏展开执行概览",
        waiting_reason: "已排队或等待名额，暂无执行信号。",
        offline_reason: "该成员暂无实时在场信号，视为未在岗。",
        unknown_reason: "无法从投影判定执行状态。",
        clock_time: "{{time}}",
        elapsed_sec: "{{count}} 秒",
        elapsed_min: "{{count}} 分钟",
        elapsed_hour: "{{count}} 小时",
        disconnected: "连接已断开 · 保留最后数据",
        data_expired: "数据可能已过期",
        synced: "实时",
        last_sync: "同步于 {{time}}",
        group_active: "执行中",
        group_waiting: "等待中",
        group_finished: "已完成",
        group_idle: "空闲",
        status: { queued: "排队", waiting: "等待中", running: "执行中", cancelling: "取消中", done: "完成", failed: "失败", retrying: "重试中", stale: "停滞", idle: "空闲", offline: "离线", unknown: "未知" },
        action: { waiting: "等待开始当前任务", working: "正在执行当前任务", cancelling: "已请求取消", recent_done: "最近任务已完成", recent_failed: "最近任务执行失败", retrying: "正在重试当前任务", stale: "执行状态已过期", idle: "当前没有可领取的小任务", offline: "无实时信号，视为未在岗", unknown: "执行状态未知" },
      } } }))
        .replace("{{location}}", values?.location ?? "")
        .replace("{{name}}", values?.name ?? "")
        .replace("{{count}}", String(values?.count ?? ""))
        .replace("{{time}}", values?.time ?? "")
        .replace("{{anomaly}}", String(values?.anomaly ?? ""))
        .replace("{{running}}", String(values?.running ?? ""))
        .replace("{{queued}}", String(values?.queued ?? ""))
        .replace("{{total}}", String(values?.total ?? "")),
  }),
}));


const agents = researchExecutionPanelFixture.map((agent) =>
  agent.id === "running" || agent.id === "failed"
    ? { ...agent, currentNodeId: `node-${agent.id}` }
    : agent,
);

// Legacy fixture statuses → overlay state mapping (queued/idle stay distinct).
describe("ResearchExecutionPanel", () => {
  it("shows the mixed roster upgraded to the execution overlay", () => {
    render(<ResearchExecutionPanel agents={agents} />);
    expect(screen.getByText(/2 异常 · 1 运行 · 1 排队 · 6 人/)).toBeTruthy();
    const statuses = new Set(
      screen.getAllByTestId("execution-overlay-row").map(
        (row) => row.getAttribute("data-status") as string,
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
    const idle = screen.getAllByRole("button", { name: /查看苏澄最近活动/ })[0]!;
    idle.focus();
    fireEvent.click(idle);
    expect(screen.getByText("当前没有可领取的小任务。")).toBeTruthy();
    expect(screen.getByText("暂无可定位节点")).toBeTruthy();
    expect(onLocate).not.toHaveBeenCalled();
    expect(document.activeElement).toBe(idle);
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
    const running = screen.getAllByTestId("execution-overlay-row").find((row) => row.dataset.status === "running");
    expect(running?.querySelector(".animate-nav-progress-sweep")?.className).toContain("motion-reduce:hidden");
    expect(container.firstElementChild?.className).toContain("min-w-0");
    expect(container.querySelector(".grid-cols-\\[auto_minmax\\(0\\,1fr\\)_auto\\]")).toBeTruthy();
  });
});
