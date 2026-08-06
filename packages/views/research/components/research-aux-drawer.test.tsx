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
          module_sources: "调研依据与协作分工",
          module_sources_desc: "依据从哪里来 · 谁负责什么 · 现在齐了没有",
          module_detail: "节点详情",
          aux_close: "关闭面板",
          aux_close_sources: "关闭调研依据与协作分工",
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
    expect(label).toHaveTextContent("调研依据与协作分工");
    expect(screen.getByRole("complementary", { name: "调研依据与协作分工" })).toBe(panel);
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

  it("restores focus to a trigger that unmounts while the drawer is open (LRM-1177)", async () => {
    // Shared useOverlayPanelA11y contract: opener may unmount for the whole
    // open window (chat FAB path). Aux drawer uses the same hook — cover the
    // remount+testid re-locate path here so chat-only coverage cannot regress
    // the aux consumer silently.
    function Harness() {
      const [panel, setPanel] = useState<"trajectory" | null>(null);
      return (
        <div>
          {panel ? null : (
            <button
              type="button"
              data-testid="research-module-trajectory"
              onClick={() => setPanel("trajectory")}
            >
              open trajectory
            </button>
          )}
          <ResearchAuxDrawer panel={panel} onClose={() => setPanel(null)}>
            <span>body</span>
          </ResearchAuxDrawer>
        </div>
      );
    }
    render(<Harness />);
    const opener = screen.getByTestId("research-module-trajectory");
    opener.focus();
    await userEvent.click(opener);

    const drawer = screen.getByTestId("research-aux-drawer");
    expect(drawer.contains(document.activeElement)).toBe(true);
    expect(opener.isConnected).toBe(false);

    await userEvent.keyboard("{Escape}");
    const remounted = screen.getByTestId("research-module-trajectory");
    expect(remounted).not.toBe(opener);
    expect(document.activeElement).toBe(remounted);
  });

  it("keeps focusable controls inside the open desktop drawer (LRM-1290)", () => {
    render(
      <ResearchAuxDrawer panel="detail" onClose={() => {}}>
        <button type="button">inside</button>
      </ResearchAuxDrawer>,
    );
    const drawer = screen.getByTestId("research-aux-drawer");
    expect(drawer.contains(document.activeElement)).toBe(true);
    expect(document.activeElement).not.toBe(document.body);
    const focusables = drawer.querySelectorAll<HTMLElement>(
      'button:not([disabled]), [href], input:not([disabled]), select:not([disabled]), textarea:not([disabled]), [tabindex]:not([tabindex="-1"])',
    );
    expect(focusables.length).toBeGreaterThan(0);
  });

  it("marks the close lucide decorative under the labeled button (LRM-1290)", () => {
    render(
      <ResearchAuxDrawer panel="trajectory" onClose={() => {}}>
        <span>body</span>
      </ResearchAuxDrawer>,
    );
    const close = screen.getByRole("button", { name: "关闭面板" });
    const icon = close.querySelector("svg");
    expect(icon).toHaveAttribute("aria-hidden", "true");
  });

  it("sources panel shows three-question desc + panel-specific close name (LRM-1329)", () => {
    render(
      <ResearchAuxDrawer panel="sources" onClose={() => {}}>
        <span>body</span>
      </ResearchAuxDrawer>,
    );
    expect(screen.getByTestId("research-aux-drawer-desc")).toHaveTextContent(
      "依据从哪里来 · 谁负责什么 · 现在齐了没有",
    );
    expect(
      screen.getByRole("button", { name: "关闭调研依据与协作分工" }),
    ).toBeTruthy();
  });

  it("keeps focus put when an unmounted aux trigger has no relocatable key (LRM-1177)", async () => {
    function Harness() {
      const [panel, setPanel] = useState<"sources" | null>(null);
      return (
        <div>
          {panel ? null : (
            <button type="button" onClick={() => setPanel("sources")}>
              open sources
            </button>
          )}
          <ResearchAuxDrawer panel={panel} onClose={() => setPanel(null)}>
            <span>body</span>
          </ResearchAuxDrawer>
        </div>
      );
    }
    render(<Harness />);
    const trigger = screen.getByRole("button", { name: "open sources" });
    trigger.focus();
    await userEvent.click(trigger);
    expect(
      screen.getByTestId("research-aux-drawer").contains(document.activeElement),
    ).toBe(true);

    await userEvent.keyboard("{Escape}");
    expect(document.activeElement).toBe(document.body);
  });
});
