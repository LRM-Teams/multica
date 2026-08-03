import { useState } from "react";
import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { beforeEach, describe, expect, it, vi } from "vitest";
import { ResearchAuxDrawer } from "./research-aux-drawer";

const mobileState = vi.hoisted(() => ({ isMobile: false }));

vi.mock("@multica/ui/hooks/use-mobile", () => ({
  useIsMobile: () => mobileState.isMobile,
}));

vi.mock("../../i18n/use-t", () => ({
  useT: () => ({
    t: (picker: (keys: Record<string, unknown>) => unknown) => {
      const keys = {
        panel: {
          module_trajectory: "搜索轨迹",
          module_sources: "信源策略",
          module_detail: "节点详情",
          aux_close: "关闭面板",
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
    "data-panel"?: string;
  }) => (
    <div data-testid={rest["data-testid"]} data-panel={rest["data-panel"]}>
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

describe("ResearchAuxDrawer desktop a11y (LRM-1100)", () => {
  beforeEach(() => {
    mobileState.isMobile = false;
  });

  it("closes on Escape", async () => {
    const onClose = vi.fn();
    render(
      <ResearchAuxDrawer panel="trajectory" onClose={onClose}>
        <span>body</span>
      </ResearchAuxDrawer>,
    );
    await userEvent.keyboard("{Escape}");
    expect(onClose).toHaveBeenCalledTimes(1);
  });

  it("does not fire onClose on Escape while closed", async () => {
    const onClose = vi.fn();
    render(
      <ResearchAuxDrawer panel={null} onClose={onClose}>
        <span>body</span>
      </ResearchAuxDrawer>,
    );
    await userEvent.keyboard("{Escape}");
    expect(onClose).not.toHaveBeenCalled();
  });

  it("exposes an accessible name via aria-labelledby pointing at the visible title", () => {
    render(
      <ResearchAuxDrawer panel="sources" onClose={() => {}}>
        <span>body</span>
      </ResearchAuxDrawer>,
    );
    const panel = screen.getByTestId("research-aux-drawer");
    const labelId = panel.getAttribute("aria-labelledby");
    expect(labelId).toBeTruthy();
    const label = document.getElementById(labelId as string);
    expect(label).not.toBeNull();
    expect(label).toHaveTextContent("信源策略");
    expect(screen.getByRole("complementary", { name: "信源策略" })).toBe(panel);
  });

  it("moves focus into the panel on open and restores it on close", async () => {
    function Harness() {
      const [panel, setPanel] = useState<"detail" | null>(null);
      return (
        <div>
          <button type="button" onClick={() => setPanel("detail")}>
            open
          </button>
          <ResearchAuxDrawer panel={panel} onClose={() => setPanel(null)}>
            <span>body</span>
          </ResearchAuxDrawer>
        </div>
      );
    }
    render(<Harness />);
    const trigger = screen.getByRole("button", { name: "open" });
    trigger.focus();
    await userEvent.click(trigger);

    const drawer = screen.getByTestId("research-aux-drawer");
    expect(drawer.contains(document.activeElement)).toBe(true);

    await userEvent.keyboard("{Escape}");
    expect(screen.queryByTestId("research-aux-drawer")).toBeNull();
    expect(document.activeElement).toBe(trigger);
  });
});
