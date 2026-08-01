import { render, screen } from "@testing-library/react";
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

  it("renders a side aside on desktop when open", () => {
    render(
      <ResearchChatDrawer open onClose={() => {}}>
        <span>body</span>
      </ResearchChatDrawer>,
    );
    const el = screen.getByTestId("research-chat-drawer");
    expect(el.tagName.toLowerCase()).toBe("aside");
    expect(el).toHaveAttribute("data-placement", "aside");
    expect(screen.getByText("body")).toBeInTheDocument();
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
});
