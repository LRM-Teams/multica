/**
 * LRM-1290 — desktop overlay-card Esc + focus move-in/restore via shared hook.
 */
import { useState } from "react";
import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { beforeEach, describe, expect, it, vi } from "vitest";
import type { ResearchGraphNode } from "@multica/core/types";
import { ResearchNodeDetail } from "./research-node-detail";

const mobileState = vi.hoisted(() => ({ isMobile: false }));

vi.mock("@multica/ui/hooks/use-mobile", () => ({
  useIsMobile: () => mobileState.isMobile,
}));

vi.mock("../../i18n/use-t", () => ({
  useT: () => ({
    t: (picker: (keys: Record<string, unknown>) => unknown) => {
      const keys = {
        node: {
          detail_hint: "节点详情",
          goal: "目标",
          status: { ready: "就绪", running: "进行中", done: "完成", failed: "失败" },
          confidence: "置信度",
        },
        panel: { weight: "权重" },
        overlay: { detail_close: "关闭详情" },
        content_faces: {
          goal: "目标",
          operation_approach: "操作思路",
          research_approach: "调研思路",
          result: "调研结果",
          missing: "未提供",
          result_pending: "结果整理中",
          result_pending_detail: "正在整理，暂未形成可展示结果。",
          result_failed: "本轮未产出可展示结果",
          result_failed_detail: "本轮未产出可展示结果。",
        },
      };
      return picker(keys as never);
    },
  }),
}));

vi.mock("@multica/ui/components/ui/sheet", () => ({
  Sheet: ({
    open,
    children,
  }: {
    open?: boolean;
    children?: React.ReactNode;
  }) => (open ? <div data-testid="sheet-root">{children}</div> : null),
  SheetContent: ({
    children,
    ...rest
  }: {
    children?: React.ReactNode;
    "data-testid"?: string;
    "data-placement"?: string;
  }) => (
    <div
      data-testid={rest["data-testid"]}
      data-placement={rest["data-placement"]}
    >
      {children}
    </div>
  ),
  SheetHeader: ({ children }: { children?: React.ReactNode }) => (
    <div>{children}</div>
  ),
  SheetTitle: ({ children }: { children?: React.ReactNode }) => (
    <h2>{children}</h2>
  ),
  SheetDescription: ({ children }: { children?: React.ReactNode }) => (
    <p>{children}</p>
  ),
}));

const node = {
  id: "n1",
  title: "探针节点",
  node_type: "finding",
  status: "ready",
  summary: "摘要",
  payload: {},
} as ResearchGraphNode;

describe("ResearchNodeDetail overlay-card a11y (LRM-1290)", () => {
  beforeEach(() => {
    mobileState.isMobile = false;
  });

  it("closes on Escape when onClose is provided", async () => {
    const onClose = vi.fn();
    render(
      <ResearchNodeDetail
        node={node}
        sources={[]}
        open
        placement="overlay-card"
        onClose={onClose}
      />,
    );
    await userEvent.keyboard("{Escape}");
    expect(onClose).toHaveBeenCalledTimes(1);
  });

  it("does not fire onClose on Escape while closed", async () => {
    const onClose = vi.fn();
    render(
      <ResearchNodeDetail
        node={node}
        sources={[]}
        open={false}
        placement="overlay-card"
        onClose={onClose}
      />,
    );
    await userEvent.keyboard("{Escape}");
    expect(onClose).not.toHaveBeenCalled();
  });

  it("moves focus into the card on open and restores on Esc close", async () => {
    function Harness() {
      const [open, setOpen] = useState(false);
      return (
        <div>
          <button type="button" onClick={() => setOpen(true)}>
            open detail
          </button>
          <ResearchNodeDetail
            node={node}
            sources={[]}
            open={open}
            placement="overlay-card"
            onClose={() => setOpen(false)}
          />
        </div>
      );
    }
    render(<Harness />);
    const trigger = screen.getByRole("button", { name: "open detail" });
    trigger.focus();
    await userEvent.click(trigger);

    const card = screen.getByTestId("research-node-detail");
    expect(card.contains(document.activeElement)).toBe(true);
    expect(document.activeElement).not.toBe(document.body);

    const focusables = card.querySelectorAll<HTMLElement>(
      'button:not([disabled]), [href], input:not([disabled]), select:not([disabled]), textarea:not([disabled]), [tabindex]:not([tabindex="-1"])',
    );
    expect(focusables.length).toBeGreaterThan(0);

    await userEvent.keyboard("{Escape}");
    expect(screen.queryByTestId("research-node-detail")).toBeNull();
    expect(document.activeElement).toBe(trigger);
  });

  it("close icon is decorative under the labeled button", () => {
    render(
      <ResearchNodeDetail
        node={node}
        sources={[]}
        open
        placement="overlay-card"
        onClose={() => {}}
      />,
    );
    const close = screen.getByRole("button", { name: "关闭详情" });
    const icon = close.querySelector("svg");
    expect(icon).toHaveAttribute("aria-hidden", "true");
  });
});
