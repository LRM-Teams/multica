import { fireEvent, render, screen } from "@testing-library/react";
import { describe, expect, it, vi } from "vitest";
import { researchExecutionPanelFixture, type ResearchExecutionStatus } from "../lib/research-execution-panel-fixture";
import { ResearchExecutionPanel } from "./research-execution-panel";

describe("ResearchExecutionPanel", () => {
  it("shows who is doing what across every typed status", () => {
    render(<ResearchExecutionPanel agents={researchExecutionPanelFixture} />);
    expect(screen.getByText("1 个智能体执行中")).toBeTruthy();
    for (const text of ["Lin", "交叉验证", "核验 2026 年企业版定价与合同限制", "持续 8 分钟", "可定位至 证据节点 12"]) expect(screen.getByText(text)).toBeTruthy();
    const statuses = new Set(screen.getAllByTestId("research-execution-row").map((row) => row.getAttribute("data-status") as ResearchExecutionStatus));
    expect(statuses).toEqual(new Set(["queued", "running", "done", "failed", "stale", "idle"]));
    for (const label of ["排队", "执行中", "完成", "失败", "停滞", "空闲"]) expect(screen.getAllByText(label).length).toBeGreaterThan(0);
  });

  it("expands long text and failure reason, then requests location", () => {
    const onLocate = vi.fn();
    render(<ResearchExecutionPanel agents={researchExecutionPanelFixture} onLocate={onLocate} />);
    fireEvent.click(screen.getByRole("button", { name: "展开Owen的执行详情" }));
    expect(screen.getByText("原始 CSV 包含重复表头，解析在第 482 行停止。")).toBeTruthy();
    expect(screen.getByText("无法识别日期列格式。请清洗输入后重试。")).toBeTruthy();
    fireEvent.click(screen.getByRole("button", { name: "定位至 分析节点 7" }));
    expect(onLocate).toHaveBeenCalledWith(expect.objectContaining({ id: "failed", status: "failed" }));
  });

  it("keeps text and shape semantics when running motion is reduced", () => {
    render(<ResearchExecutionPanel agents={researchExecutionPanelFixture} />);
    const runningRow = screen.getAllByTestId("research-execution-row").find((row) => row.dataset.status === "running");
    const energyFlow = runningRow?.querySelector(".animate-nav-progress-sweep");
    expect(energyFlow?.className).toContain("motion-reduce:hidden");
    expect(runningRow?.textContent).toContain("执行中");
    expect(runningRow?.querySelector("svg")).toBeTruthy();
  });

  it("guards compact and 360px layouts from long-text overflow", () => {
    const { container } = render(<ResearchExecutionPanel agents={researchExecutionPanelFixture} />);
    expect(container.firstElementChild?.className).toContain("min-w-0");
    expect(container.querySelector(".grid-cols-\\[auto_minmax\\(0\\,1fr\\)_auto\\]")).toBeTruthy();
    fireEvent.click(screen.getByRole("button", { name: "展开Ravi的执行详情" }));
    expect(container.querySelector(".break-words")).toBeTruthy();
  });
});
