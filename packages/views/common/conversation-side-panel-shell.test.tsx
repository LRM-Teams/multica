// @vitest-environment jsdom
import { render, screen, fireEvent } from "@testing-library/react";
import { describe, expect, it, vi } from "vitest";
import { ConversationSidePanelShell } from "./conversation-side-panel-shell";

describe("ConversationSidePanelShell dismiss chrome (LRM-1185 / LRM-974 gate)", () => {
  it("renders a close control on variant=page even without doneLabel", () => {
    const onClose = vi.fn();
    render(
      <ConversationSidePanelShell
        variant="page"
        onClose={onClose}
        closeAriaLabel="Close profile"
      >
        <div>body</div>
      </ConversationSidePanelShell>,
    );

    const close = screen.getByRole("button", { name: "Close profile" });
    fireEvent.click(close);
    expect(onClose).toHaveBeenCalledTimes(1);
  });

  it("gives the page fallback close a 44x44 hit target with a 20px glyph", () => {
    render(
      <ConversationSidePanelShell
        variant="page"
        onClose={vi.fn()}
        closeAriaLabel="Close profile"
      >
        <div>body</div>
      </ConversationSidePanelShell>,
    );

    const close = screen.getByTestId("side-panel-page-close");
    expect(close).toHaveClass("size-8");
    expect(close.querySelector("svg")).toHaveClass("size-4");
  });

  it("keeps the floating page header (agent profile) dismissable", () => {
    const onClose = vi.fn();
    render(
      <ConversationSidePanelShell
        variant="page"
        header="floating"
        onClose={onClose}
        closeAriaLabel="Close agent"
      >
        <div>identity row</div>
      </ConversationSidePanelShell>,
    );

    fireEvent.click(screen.getByTestId("side-panel-page-close"));
    expect(onClose).toHaveBeenCalledTimes(1);
  });

  it("still prefers the text Done control when doneLabel is supplied (LRM-494)", () => {
    render(
      <ConversationSidePanelShell
        variant="page"
        onClose={vi.fn()}
        closeAriaLabel="Close details"
        doneLabel="完成"
      >
        <div>body</div>
      </ConversationSidePanelShell>,
    );

    expect(screen.getByTestId("channel-details-done")).toHaveTextContent("完成");
    expect(screen.queryByTestId("side-panel-page-close")).toBeNull();
  });

  it("leaves the docked panel variant on the compact X", () => {
    render(
      <ConversationSidePanelShell
        variant="panel"
        onClose={vi.fn()}
        closeAriaLabel="Close dock"
      >
        <div>body</div>
      </ConversationSidePanelShell>,
    );

    expect(screen.getByRole("button", { name: "Close dock" })).toBeTruthy();
    expect(screen.queryByTestId("side-panel-page-close")).toBeNull();
  });
});
