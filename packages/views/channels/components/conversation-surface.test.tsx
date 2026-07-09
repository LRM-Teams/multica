import type { CSSProperties, ReactNode } from "react";
import { render, screen } from "@testing-library/react";
import { describe, expect, it, vi } from "vitest";
import {
  Composer,
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
