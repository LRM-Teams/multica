import { useState } from "react";
import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { beforeEach, describe, expect, it, vi } from "vitest";
import { ResearchChatDrawer } from "./research-chat-drawer";

const mobileState = vi.hoisted(() => ({ isMobile: false }));

vi.mock("@multica/ui/hooks/use-mobile", () => ({
  useIsMobile: () => mobileState.isMobile,
}));

vi.mock("../../i18n/use-t", () => ({
  useT: () => ({
    t: (picker: (keys: Record<string, unknown>) => unknown) => {
      const keys = {
        panel: { chat: "舰队对话" },
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

describe("ResearchChatDrawer", () => {
  beforeEach(() => {
    mobileState.isMobile = false;
  });

  it("renders a floating overlay on desktop when open (LRM-1061)", () => {
    render(
      <ResearchChatDrawer open onClose={() => {}}>
        <span>body</span>
      </ResearchChatDrawer>,
    );
    const el = screen.getByTestId("research-chat-drawer");
    expect(el.tagName.toLowerCase()).toBe("aside");
    // LRM-1056 v2: float, not a permanent dock that squeezes the canvas.
    expect(el.getAttribute("data-placement")).toBe("float");
    expect(screen.getByText("body")).toBeTruthy();
  });

  it("renders nothing on desktop when closed", () => {
    const { container } = render(
      <ResearchChatDrawer open={false} onClose={() => {}}>
        <span>body</span>
      </ResearchChatDrawer>,
    );
    expect(container).toBeEmptyDOMElement();
  });

  it("renders a bottom sheet shell on narrow viewports", () => {
    mobileState.isMobile = true;
    render(
      <ResearchChatDrawer open onClose={() => {}}>
        <span>body</span>
      </ResearchChatDrawer>,
    );
    const el = screen.getByTestId("research-chat-drawer");
    expect(el).toHaveAttribute("data-placement", "sheet");
    expect(screen.getByTestId("sheet-root")).toBeInTheDocument();
  });

  // LRM-1100 — the narrow Sheet branch already gets Escape/focus handling from
  // Radix; the desktop float branch had none.
  it("closes the desktop float on Escape (LRM-1100)", async () => {
    const onClose = vi.fn();
    render(
      <ResearchChatDrawer open onClose={onClose}>
        <span>body</span>
      </ResearchChatDrawer>,
    );
    await userEvent.keyboard("{Escape}");
    expect(onClose).toHaveBeenCalledTimes(1);
  });

  it("does not fire onClose on Escape while closed (LRM-1100)", async () => {
    const onClose = vi.fn();
    render(
      <ResearchChatDrawer open={false} onClose={onClose}>
        <span>body</span>
      </ResearchChatDrawer>,
    );
    await userEvent.keyboard("{Escape}");
    expect(onClose).not.toHaveBeenCalled();
  });

  it("exposes an accessible name on the desktop float (LRM-1100)", () => {
    render(
      <ResearchChatDrawer open onClose={() => {}}>
        <span>body</span>
      </ResearchChatDrawer>,
    );
    expect(screen.getByRole("complementary", { name: "舰队对话" })).toBe(
      screen.getByTestId("research-chat-drawer"),
    );
  });

  it("moves focus into the float on open and restores it on close (LRM-1100)", async () => {
    function Harness() {
      const [open, setOpen] = useState(false);
      return (
        <div>
          <button type="button" onClick={() => setOpen(true)}>
            open chat
          </button>
          <ResearchChatDrawer open={open} onClose={() => setOpen(false)}>
            <button type="button">send</button>
          </ResearchChatDrawer>
        </div>
      );
    }
    render(<Harness />);
    const trigger = screen.getByRole("button", { name: "open chat" });
    trigger.focus();
    await userEvent.click(trigger);

    const drawer = screen.getByTestId("research-chat-drawer");
    expect(drawer.contains(document.activeElement)).toBe(true);

    await userEvent.keyboard("{Escape}");
    expect(screen.queryByTestId("research-chat-drawer")).toBeNull();
    expect(document.activeElement).toBe(trigger);
  });

  it("restores focus to a trigger that unmounts while the float is open (LRM-1177)", async () => {
    // Mirrors research-canvas.tsx: `chatFab` is `!chatOpen && ... ? <Fab/> : null`,
    // so the only desktop opener is UNMOUNTED for the whole time the float is
    // open and comes back as a different DOM node. Restoring by node identity
    // alone silently no-ops on exactly this path.
    function Harness() {
      const [open, setOpen] = useState(false);
      return (
        <div>
          {open ? null : (
            <button
              type="button"
              data-testid="research-canvas-chat-fab"
              onClick={() => setOpen(true)}
            >
              open chat
            </button>
          )}
          <ResearchChatDrawer open={open} onClose={() => setOpen(false)}>
            <button type="button">send</button>
          </ResearchChatDrawer>
        </div>
      );
    }
    render(<Harness />);
    const fab = screen.getByTestId("research-canvas-chat-fab");
    fab.focus();
    await userEvent.click(fab);

    const drawer = screen.getByTestId("research-chat-drawer");
    expect(drawer.contains(document.activeElement)).toBe(true);
    expect(fab.isConnected).toBe(false);

    await userEvent.keyboard("{Escape}");
    const remounted = screen.getByTestId("research-canvas-chat-fab");
    expect(remounted).not.toBe(fab);
    expect(document.activeElement).toBe(remounted);
  });

  it("keeps focus put when an unmounted trigger has no relocatable key (LRM-1177)", async () => {
    function Harness() {
      const [open, setOpen] = useState(false);
      return (
        <div>
          {open ? null : (
            <button type="button" onClick={() => setOpen(true)}>
              open chat
            </button>
          )}
          <ResearchChatDrawer open={open} onClose={() => setOpen(false)}>
            <button type="button">send</button>
          </ResearchChatDrawer>
        </div>
      );
    }
    render(<Harness />);
    const trigger = screen.getByRole("button", { name: "open chat" });
    trigger.focus();
    await userEvent.click(trigger);
    expect(
      screen.getByTestId("research-chat-drawer").contains(document.activeElement),
    ).toBe(true);

    await userEvent.keyboard("{Escape}");
    // No id / data-testid to re-find: fall back to doing nothing rather than
    // grabbing an unrelated control.
    expect(document.activeElement).toBe(document.body);
  });
});
