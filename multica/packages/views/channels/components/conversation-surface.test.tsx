import type { CSSProperties, ReactNode } from "react";
import { render, screen } from "@testing-library/react";
import { describe, expect, it, vi } from "vitest";
import {
  Composer,
  ConversationHeader,
  getMobileVisualViewportStyle,
  MobileThreadDrawerContent,
} from "./conversation-surface";

vi.mock("@multica/ui/components/ui/drawer", () => ({
  DrawerContent: ({ children, style }: { children: ReactNode; style?: CSSProperties }) => (
    <div data-testid="drawer-content" style={style}>
      {children}
    </div>
  ),
}));

describe("mobile thread drawer viewport sizing", () => {
  it("uses the visible viewport rectangle for mobile drawer content", () => {
    expect(getMobileVisualViewportStyle({ height: 512.4, offsetTop: 87.6 })).toEqual({
      top: 88,
      bottom: "auto",
      height: 512,
      maxHeight: 512,
    });
  });

  it("leaves drawer sizing to CSS when the drawer is closed", () => {
    render(<MobileThreadDrawerContent open={false}>Thread</MobileThreadDrawerContent>);

    expect(screen.getByTestId("drawer-content")).not.toHaveStyle({ height: "512px" });
  });
});

describe("ConversationHeader — title column flex (LRM-279)", () => {
  it("gives the title cluster flex-1 min-w-0 so it fills space before actions", () => {
    const { container } = render(
      <ConversationHeader
        isMobile={false}
        leading={<span data-testid="leading">←</span>}
        title={<button type="button"># long-channel-name</button>}
        actions={<button type="button">Search</button>}
      />,
    );

    const header = container.querySelector("header");
    expect(header).not.toHaveClass("justify-between");

    const titleColumn = screen.getByTestId("leading").parentElement;
    expect(titleColumn).toHaveClass("flex-1", "min-w-0");

    const titleWrapper = screen.getByRole("button", { name: "# long-channel-name" }).parentElement;
    expect(titleWrapper).toHaveClass("flex-1", "min-w-0", "overflow-hidden");
  });

  it("keeps status shrink-0 so a long title cannot paint over Working", () => {
    render(
      <ConversationHeader
        isMobile
        leading={<span data-testid="leading">←</span>}
        title={<span>Multica Dev 全栈工程师</span>}
        status={<span data-testid="live-cue">Working</span>}
        actions={<button type="button">Search</button>}
      />,
    );

    const statusSlot = screen.getByTestId("conversation-header-status");
    expect(statusSlot).toHaveClass("shrink-0");
    expect(statusSlot).toContainElement(screen.getByTestId("live-cue"));
  });
});

describe("ConversationHeader — slots3 layout (LRM-447)", () => {
  it("uses a three-column grid on desktop slots3", () => {
    render(
      <ConversationHeader
        isMobile={false}
        layout="slots3"
        leading={<span data-testid="meta-tile">#</span>}
        title={<button type="button">Dev group</button>}
        meta="12 members · 3 agents"
        actions={<div data-testid="action-rail">rail</div>}
      />,
    );

    const header = screen.getByTestId("conversation-header-slots3");
    expect(header).toHaveClass("grid");
    expect(screen.getByTestId("meta-tile")).toBeInTheDocument();
    expect(screen.getByTestId("action-rail")).toBeInTheDocument();
    expect(screen.getByText("12 members · 3 agents")).toBeInTheDocument();
  });

  it("keeps the flex row on mobile even when layout=slots3", () => {
    const { container } = render(
      <ConversationHeader
        isMobile
        layout="slots3"
        leading={<span data-testid="leading">←</span>}
        title="Mobile title"
        actions={<button type="button">More</button>}
      />,
    );

    expect(screen.queryByTestId("conversation-header-slots3")).toBeNull();
    expect(container.querySelector("header")).toHaveClass("flex");
  });
});

describe("Composer (re-exported from conversation-surface)", () => {
  it("keeps editor media in the scroll area while actions stay fixed below it", () => {
    render(
      <Composer
        surface="channel"
        editor={<div data-testid="composer-editor">Editor</div>}
        sendLabel="Send"
        sendDisabled={false}
        onSend={vi.fn()}
        isMobile={false}
        leadingActions={<button type="button">Attach</button>}
      />,
    );

    const shell = screen.getByTestId("composer-editor").closest('[data-slot="composer-shell"]');
    const editorScroll = screen.getByTestId("composer-editor").closest('[data-slot="composer-editor-scroll"]');
    const actionRow = screen.getByText("Attach").closest('[data-slot="composer-action-row"]');

    expect(shell).not.toBeNull();
    expect(editorScroll).not.toBeNull();
    expect(actionRow).not.toBeNull();
    expect(editorScroll).not.toContainElement(actionRow as HTMLElement);
    expect(shell).toContainElement(actionRow as HTMLElement);
  });
});
