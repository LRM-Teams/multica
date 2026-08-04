// @vitest-environment jsdom

import { screen } from "@testing-library/react";
import { cloneElement, isValidElement } from "react";
import {
  SidebarMenuAction,
  SidebarMenuButton,
  SidebarMenuItem,
  SidebarProvider,
} from "@multica/ui/components/ui/sidebar";
import userEvent from "@testing-library/user-event";
import { describe, expect, it, vi } from "vitest";
import { renderWithI18n } from "../test/i18n";
import { RuntimeAttentionAlert } from "./runtime-attention-alert";

const mockCount = vi.hoisted(() => ({ current: 0 }));

vi.mock("@multica/core/runtimes/hooks", () => ({
  useMyAttentionRuntimeSummary: () => ({
    count: mockCount.current,
    firstRuntimeId: mockCount.current > 0 ? "rt-mine" : null,
  }),
}));

vi.mock("@multica/core/paths", () => ({
  useWorkspacePaths: () => ({
    computersAttention: (runtimeId: string) =>
      `/acme/computers?attention_runtime=${runtimeId}`,
  }),
}));

vi.mock("../navigation/app-link", () => ({
  AppLink: ({ href, children, ...rest }: { href: string; children: React.ReactNode }) => (
    <a href={href} {...rest}>
      {children}
    </a>
  ),
}));

// jsdom can't position a real floating-ui popup; render trigger/content
// directly (matches the established pattern in
// issues/components/issue-agent-header-chip.test.tsx) so the test stays on
// what THIS component decides, not on Base UI's positioning internals.
vi.mock("@multica/ui/components/ui/popover", () => ({
  Popover: ({ children }: { children: React.ReactNode }) => <div>{children}</div>,
  PopoverTrigger: ({
    children,
    render,
    ...rest
  }: { children: React.ReactNode; render?: React.ReactElement } & Record<string, unknown>) =>
    isValidElement(render)
      ? cloneElement(render, rest, children)
      : <button type="button" {...rest}>{children}</button>,
  PopoverContent: ({ children }: { children: React.ReactNode }) => (
    <div data-testid="runtime-attention-content">{children}</div>
  ),
}));

describe("RuntimeAttentionAlert", () => {
  it("renders nothing when no runtime needs attention", () => {
    mockCount.current = 0;
    const { container } = renderWithI18n(<RuntimeAttentionAlert wsId="ws-1" />);
    expect(container).toBeEmptyDOMElement();
  });

  it("renders the warning icon and singular count text for exactly one machine", async () => {
    mockCount.current = 1;
    renderWithI18n(<RuntimeAttentionAlert wsId="ws-1" />);
    expect(
      screen.getByRole("button", { name: /1 machine needs an update/i }),
    ).toBeInTheDocument();
    expect(screen.getByText("1 machine has an available update.")).toBeInTheDocument();
  });

  it("pluralizes for more than one machine", async () => {
    mockCount.current = 3;
    renderWithI18n(<RuntimeAttentionAlert wsId="ws-1" />);
    expect(screen.getByText("3 machines have available updates.")).toBeInTheDocument();
  });

  it("links the view action to the computers page, never embeds an upgrade action here (task #9: tell + take you there, act elsewhere)", () => {
    mockCount.current = 2;
    renderWithI18n(<RuntimeAttentionAlert wsId="ws-1" />);
    const link = screen.getByRole("link", { name: "View" });
    expect(link).toHaveAttribute(
      "href",
      "/acme/computers?attention_runtime=rt-mine",
    );
    // Scoped to the popover content only — the trigger button itself is
    // legitimately labeled "N machines need updates", that's not the thing
    // being ruled out here.
    const content = screen.getByTestId("runtime-attention-content");
    expect(content.querySelector("button")).toBeNull();
  });

  it("keeps the warning action outside the sidebar row link for mouse and keyboard activation", async () => {
    mockCount.current = 1;
    const outerClick = vi.fn((e: React.MouseEvent) => e.preventDefault());
    const consoleError = vi.spyOn(console, "error").mockImplementation(() => undefined);
    const { container } = renderWithI18n(
      <div>
        <SidebarProvider>
          <SidebarMenuItem>
            <SidebarMenuButton render={<a href="/acme/computers" onClick={outerClick} />}>
              Computers
            </SidebarMenuButton>
            <RuntimeAttentionAlert
              wsId="ws-1"
              trigger={<SidebarMenuAction />}
            />
          </SidebarMenuItem>
        </SidebarProvider>
      </div>,
    );
    expect(container.querySelector("a a, a button")).toBeNull();

    const trigger = screen.getByRole("button", { name: /1 machine needs an update/i });
    await userEvent.click(trigger);
    await userEvent.keyboard("{Enter}");
    expect(outerClick).not.toHaveBeenCalled();
    expect(consoleError).not.toHaveBeenCalled();
    consoleError.mockRestore();
  });
});
