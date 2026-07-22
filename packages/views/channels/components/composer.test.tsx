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

vi.mock("../../i18n/use-t", () => ({
  useT: () => ({
    t: (selector: (resources: {
      composer: {
        voice_start: string;
        voice_stop: string;
        voice_processing: string;
        voice_blocked: string;
      };
    }) => string) => selector({
      composer: {
        voice_start: "Record voice message",
        voice_stop: "Stop recording",
        voice_processing: "Processing voice message",
        voice_blocked: "Finish the current draft first",
      },
    }),
  }),
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

  it("renders the shared microphone immediately beside Send", () => {
    render(
      <Composer
        surface="channel"
        {...baseProps}
        sendDisabled
        voicePlaybackScope="channel-1:main"
        onVoiceSend={vi.fn(() => true)}
      />,
    );

    const microphone = screen.getByRole("button", { name: "Record voice message" });
    const send = screen.getByRole("button", { name: /send/i });
    const submitActions = send.closest('[data-slot="composer-submit-actions"]');
    expect(microphone).toBeInTheDocument();
    expect(submitActions).not.toBeNull();
    expect(submitActions).toContainElement(microphone);
    expect(microphone.nextElementSibling).toBe(send);
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

  it("mounts a prefix above tray and editor for quote previews", () => {
    render(
      <Composer
        surface="channel"
        {...baseProps}
        sendDisabled={false}
        prefix={<div data-testid="composer-prefix-content">quote</div>}
        tray={<div data-testid="composer-tray-content">tray</div>}
      />,
    );

    const shell = screen.getByTestId("composer-editor").closest('[data-slot="composer-shell"]');
    const prefix = screen.getByTestId("composer-prefix-content");
    const tray = screen.getByTestId("composer-tray-content").closest('[data-slot="composer-tray"]');
    expect(shell?.firstElementChild).toBe(prefix);
    expect(prefix.compareDocumentPosition(tray as HTMLElement) & Node.DOCUMENT_POSITION_FOLLOWING).toBeTruthy();
  });

  // LRM-205: composer toolbar keeps attach only — no # / warning-icon chrome.
  it("LRM-205: action row accepts paperclip-only leading actions without a # control", () => {
    render(
      <Composer
        surface="channel"
        {...baseProps}
        sendDisabled={false}
        leadingActions={
          <button type="button" aria-label="Attach file">
            Attach
          </button>
        }
      />,
    );
    const row = screen.getByRole("button", { name: /attach file/i }).closest(
      '[data-slot="composer-action-row"]',
    );
    expect(row).not.toBeNull();
    expect(screen.queryByRole("button", { name: /reference issue|#|警/i })).toBeNull();
    expect(screen.getByRole("button", { name: /send/i })).toBeInTheDocument();
  });
});
