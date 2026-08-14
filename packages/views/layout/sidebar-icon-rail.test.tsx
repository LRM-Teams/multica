import { render, screen } from "@testing-library/react";
import { beforeEach, describe, expect, it } from "vitest";
import {
  Sidebar,
  SidebarContent,
  SidebarFooter,
  SidebarGroup,
  SidebarGroupLabel,
  SidebarHeader,
  SidebarMenu,
  SidebarMenuButton,
  SidebarMenuItem,
  SidebarProvider,
} from "@multica/ui/components/ui/sidebar";

function stubDesktopViewport() {
  Object.defineProperty(window, "innerWidth", {
    configurable: true,
    value: 1280,
    writable: true,
  });
  Object.defineProperty(window, "matchMedia", {
    configurable: true,
    writable: true,
    value: (query: string) => ({
      matches: false,
      media: query,
      addEventListener: () => {},
      removeEventListener: () => {},
    }),
  });
}

function CollapsedRail() {
  return (
    <SidebarProvider defaultOpen={false}>
      <Sidebar variant="inset" collapsible="icon">
        <SidebarHeader>
          <SidebarMenu>
            <SidebarMenuItem>
              <SidebarMenuButton data-testid="workspace-button">
                <span>L</span>
                <span>Workspace</span>
              </SidebarMenuButton>
            </SidebarMenuItem>
          </SidebarMenu>
        </SidebarHeader>
        <SidebarContent>
          <SidebarGroup>
            <SidebarGroupLabel>Workspace</SidebarGroupLabel>
            <SidebarMenu>
              <SidebarMenuItem>
                <SidebarMenuButton data-testid="inbox-button">
                  <span>icon</span>
                  <span>Inbox</span>
                  <span>3</span>
                </SidebarMenuButton>
              </SidebarMenuItem>
            </SidebarMenu>
          </SidebarGroup>
        </SidebarContent>
        <SidebarFooter>
          <div
            data-testid="help-row"
            className="flex justify-end group-data-[collapsible=icon]:justify-center"
          >
            <button type="button">Help</button>
          </div>
        </SidebarFooter>
      </Sidebar>
    </SidebarProvider>
  );
}

describe("collapsed sidebar icon rail", () => {
  beforeEach(() => {
    stubDesktopViewport();
    localStorage.clear();
  });

  it("marks the desktop rail as an icon column", () => {
    const { container } = render(<CollapsedRail />);
    const rail = container.querySelector("[data-collapsible='icon']");
    expect(rail).not.toBeNull();
  });

  it("centers icon buttons and hides leftover label/badge siblings", () => {
    render(<CollapsedRail />);
    const button = screen.getByTestId("inbox-button");
    expect(button.className).toContain("group-data-[collapsible=icon]:mx-auto");
    expect(button.className).toContain("group-data-[collapsible=icon]:justify-center");
    expect(button.className).toContain("group-data-[collapsible=icon]:p-0!");
    expect(button.className).toContain("group-data-[collapsible=icon]:[&>:not(:first-child)]:hidden");
  });

  it("centers header, groups, and footer on the same axis", () => {
    const { container } = render(<CollapsedRail />);
    const header = container.querySelector("[data-slot='sidebar-header']");
    const group = container.querySelector("[data-slot='sidebar-group']");
    const footer = container.querySelector("[data-slot='sidebar-footer']");
    expect(header?.className).toContain("group-data-[collapsible=icon]:items-center");
    expect(group?.className).toContain("group-data-[collapsible=icon]:items-center");
    expect(footer?.className).toContain("group-data-[collapsible=icon]:items-center");
    expect(screen.getByTestId("help-row").className).toContain(
      "group-data-[collapsible=icon]:justify-center",
    );
  });

  it("removes section labels from the icon column instead of leaving a collapsed gap", () => {
    const { container } = render(<CollapsedRail />);
    const label = container.querySelector("[data-slot='sidebar-group-label']");
    expect(label?.className).toContain("group-data-[collapsible=icon]:hidden");
    expect(label?.className).not.toContain("group-data-[collapsible=icon]:-mt-8");
  });

  it("keeps the collapsed inset rail at the icon token width", () => {
    const { container } = render(<CollapsedRail />);
    const wrapper = container.querySelector("[data-slot='sidebar-wrapper']");
    const gap = container.querySelector("[data-slot='sidebar-gap']");
    const box = container.querySelector("[data-slot='sidebar-container']");
    expect(wrapper).toHaveStyle({ "--sidebar-width-icon": "3rem" });
    expect(gap?.className).toContain("group-data-[collapsible=icon]:w-(--sidebar-width-icon)");
    expect(gap?.className).not.toContain("spacing(4)");
    expect(box?.className).toContain("group-data-[collapsible=icon]:w-(--sidebar-width-icon)");
    expect(box?.className).toContain("group-data-[collapsible=icon]:p-0");
  });
});
