import type { CSSProperties, HTMLAttributes, ReactNode } from "react";
import { render, screen } from "@testing-library/react";
import { describe, expect, it, vi } from "vitest";
import { getMobileVisualViewportStyle, MobileThreadDrawerContent } from "./conversation-surface";

vi.mock("@multica/ui/components/ui/drawer", () => ({
  DrawerContent: ({
    children,
    style,
    ...props
  }: { children: ReactNode; style?: CSSProperties } & HTMLAttributes<HTMLDivElement>) => (
    <div data-testid="drawer-content" style={style} {...props}>
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

  it("lets the thread body own vertical touch scrolling", () => {
    render(<MobileThreadDrawerContent open={false}>Thread</MobileThreadDrawerContent>);

    const drawer = screen.getByTestId("drawer-content");
    expect(drawer).toHaveAttribute("data-vaul-no-drag", "");
    expect(drawer).toHaveStyle({ touchAction: "pan-y" });
  });
});
