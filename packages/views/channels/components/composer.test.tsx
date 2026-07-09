import type { CSSProperties, ReactNode } from "react";
import { render, screen } from "@testing-library/react";
import { describe, expect, it, vi } from "vitest";
import { Composer } from "./composer";

vi.mock("@multica/ui/components/ui/drawer", () => ({
  DrawerContent: ({ children, style }: { children: ReactNode; style?: CSSProperties }) => (
    <div data-testid="drawer-content" style={style}>
      {children}
    </div>
  ),
}));

describe("Composer", () => {
  const baseProps = {
    editor: <div data-testid="composer-editor">Editor</div>,
    sendLabel: "Send",
    onSend: vi.fn(),
    isMobile: false,
  };

  it("renders the editor, action row and send control for a surface", () => {
    render(<Composer surface="channel" {...baseProps} sendDisabled={false} />);

    const shell = screen
      .getByTestId("composer-editor")
      .closest('[data-slot="composer-shell"]');
    const editorScroll = screen
      .getByTestId("composer-editor")
      .closest('[data-slot="composer-editor-scroll"]');
    expect(shell).not.toBeNull();
    expect(editorScroll).not.toBeNull();
    expect(shell).toHaveAttribute("data-composer-surface", "channel");
    expect(screen.getByRole("button", { name: /send/i })).toBeInTheDocument();
  });

  it("tags each of the 4 surfaces so the same shell renders everywhere", () => {
    for (const surface of ["channel", "dm_channel", "legacy_dm", "thread"] as const) {
      const { unmount } = render(
        <Composer surface={surface} {...baseProps} sendDisabled={false} />,
      );
      expect(
        screen.getByTestId("composer-editor").closest('[data-slot="composer-shell"]'),
      ).toHaveAttribute("data-composer-surface", surface);
      unmount();
    }
  });

  it("disables Send while a draft is empty or a send is in flight", () => {
    const { rerender } = render(
      <Composer surface="channel" {...baseProps} sendDisabled={true} />,
    );
    expect(screen.getByRole("button", { name: /send/i })).toBeDisabled();

    rerender(<Composer surface="channel" {...baseProps} sendDisabled={false} sending />);
    expect(screen.getByRole("button", { name: /send/i })).toBeDisabled();
  });

  it("read-only surface shows a banner instead of an editable input", () => {
    render(
      <Composer
        surface="channel"
        {...baseProps}
        sendDisabled
        readOnly
        readOnlyContent={<span>This conversation is read only</span>}
      />,
    );
    expect(screen.getByText("This conversation is read only")).toBeInTheDocument();
    expect(screen.queryByTestId("composer-editor")).not.toBeInTheDocument();
    expect(screen.queryByRole("button", { name: /send/i })).not.toBeInTheDocument();
  });

  it("mounts the tray above the input without displacing the Send control", () => {
    render(
      <Composer
        surface="channel"
        {...baseProps}
        sendDisabled={false}
        tray={<div data-testid="composer-tray-content">tray</div>}
      />,
    );
    const tray = screen
      .getByTestId("composer-tray-content")
      .closest('[data-slot="composer-tray"]');
    const editorScroll = screen
      .getByTestId("composer-editor")
      .closest('[data-slot="composer-editor-scroll"]');
    expect(tray).not.toBeNull();
    // The tray is a mount point, not part of the scrollable editor area.
    expect(editorScroll).not.toContainElement(tray as HTMLElement);
    // Send stays reachable alongside the tray.
    expect(screen.getByRole("button", { name: /send/i })).toBeInTheDocument();
  });
});
