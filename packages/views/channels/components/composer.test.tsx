import { readFileSync } from "node:fs";
import { dirname, resolve } from "node:path";
import { fileURLToPath } from "node:url";
import type { CSSProperties, ReactNode } from "react";
import { render, screen } from "@testing-library/react";
import { describe, expect, it, vi } from "vitest";
import { COMPOSER_SHELL_CLASSNAME, Composer } from "./composer";

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
        voice_uploading: string;
        voice_blocked_uploading: string;
        voice_blocked_pending_voice: string;
        voice_blocked_sending: string;
        voice_blocked_text_draft: string;
        voice_blocked_attachment_draft: string;
        voice_blocked_text_and_attachment_draft: string;
      };
    }) => string) => selector({
      composer: {
        voice_start: "Record voice message",
        voice_stop: "Stop recording",
        voice_uploading: "Uploading voice message",
        // Distinct sentinels — asserting the RIGHT one appears is only
        // meaningful if a wrong one would look different. (The retired
        // `voice_blocked` sentinel lived here saying "Finish the current draft
        // first", which never matched the real string it stood in for.)
        voice_blocked_uploading: "COPY_UPLOADING",
        voice_blocked_pending_voice: "COPY_PENDING_VOICE",
        voice_blocked_sending: "COPY_SENDING",
        voice_blocked_text_draft: "COPY_TEXT",
        voice_blocked_attachment_draft: "COPY_ATTACHMENT",
        voice_blocked_text_and_attachment_draft: "COPY_TEXT_AND_ATTACHMENT",
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

  it("keeps empty-state editor scroll short (LRM-491 Slack density)", () => {
    const { rerender } = render(
      <Composer surface="channel" {...baseProps} sendDisabled={false} isMobile />,
    );
    let editorScroll = screen
      .getByTestId("composer-editor")
      .closest('[data-slot="composer-editor-scroll"]');
    expect(editorScroll?.className).toContain("min-h-11");
    expect(editorScroll?.className).not.toContain("min-h-16");

    rerender(<Composer surface="channel" {...baseProps} sendDisabled={false} isMobile={false} />);
    editorScroll = screen
      .getByTestId("composer-editor")
      .closest('[data-slot="composer-editor-scroll"]');
    expect(editorScroll?.className).toContain("min-h-12");
    expect(editorScroll?.className).not.toContain("min-h-16");
  });

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
        voiceChannelId="channel-1"
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

  describe("LRM-353 semantic tokens", () => {
    it("shell uses border-input + bg-card + brand focus ring (no light-only hex)", () => {
      expect(COMPOSER_SHELL_CLASSNAME).toContain("border-input");
      expect(COMPOSER_SHELL_CLASSNAME).toContain("bg-card");
      expect(COMPOSER_SHELL_CLASSNAME).toContain("focus-within:ring-brand/30");
      expect(COMPOSER_SHELL_CLASSNAME).not.toMatch(/#f4f4f4/);
      expect(COMPOSER_SHELL_CLASSNAME).not.toMatch(/rgba\(29/);

      render(<Composer surface="channel" {...baseProps} sendDisabled={false} />);
      const shell = screen
        .getByTestId("composer-editor")
        .closest('[data-slot="composer-shell"]');
      expect(shell?.className).toContain("border-input");
      expect(shell?.className).toContain("bg-card");
      expect(shell?.className).toContain("focus-within:ring-brand/30");
    });

    it("composer source and placeholder CSS stay on semantic tokens", () => {
      const here = dirname(fileURLToPath(import.meta.url));
      const composerSrc = readFileSync(resolve(here, "composer.tsx"), "utf8");
      expect(composerSrc).not.toMatch(/#f4f4f4/);
      expect(composerSrc).not.toMatch(/hover:bg-\[#/);
      expect(composerSrc).toContain("border-input");
      expect(composerSrc).toContain("bg-card");

      const placeholderCss = readFileSync(
        resolve(here, "../../editor/styles/shell.css"),
        "utf8",
      );
      expect(placeholderCss).toMatch(/color:\s*var\(--muted-foreground\)/);
    });

    it("leading actions inherit muted-foreground for attach/mic icons", () => {
      render(
        <Composer
          surface="thread"
          {...baseProps}
          sendDisabled={false}
          leadingActions={<button type="button" aria-label="Attach">📎</button>}
          voiceChannelId="thread-1"
          voicePlaybackScope="thread-1"
          onVoiceSend={vi.fn(() => true)}
        />,
      );
      const leading = screen
        .getByRole("button", { name: /attach/i })
        .closest('[data-slot="composer-leading-actions"]');
      expect(leading?.className).toContain("text-muted-foreground");
      const submit = screen
        .getByRole("button", { name: "Record voice message" })
        .closest('[data-slot="composer-submit-actions"]');
      expect(submit?.className).toContain("text-muted-foreground");
      expect(screen.getByRole("button", { name: /send/i })).toBeInTheDocument();
    });
  });
});

// #858 — every reachable cause of a disabled mic renders a VISIBLE explanation,
// on all four surfaces that have a mic. The retired `composer.voice_blocked`
// said one sentence for every cause, so it was true for at most one of them;
// worse, it lived in a `title`, and a natively-disabled button fires no hover
// events, so it reached nobody at all.
describe("Composer — voice block reason (#858)", () => {
  const base = {
    editor: <div data-testid="composer-editor">Editor</div>,
    sendLabel: "Send",
    onSend: vi.fn(),
    isMobile: false,
    sendDisabled: false,
    voiceChannelId: "chan-1",
    voicePlaybackScope: "scope-1",
    onVoiceSend: () => true,
  };

  const NONE = { hasTextDraft: false, hasAttachmentDraft: false };

  function status() {
    return document.querySelector('[data-slot="composer-voice-block-status"]');
  }
  function mic() {
    return screen.getByRole("button", { name: "Record voice message" });
  }

  // All four surfaces that mount a mic. DMs were NOT in the original contract
  // and were found by grepping the call sites: had they been left out while
  // `voice_blocked` was retired, their mics would have gone grey with no
  // explanation at all — the very defect this ticket removes.
  const SURFACES = ["channel", "thread", "dm_channel", "legacy_dm"] as const;

  describe.each(SURFACES)("surface=%s", (surface) => {
    it("explains a blocked mic in a visible role=status the mic points at, and keeps the mic natively disabled", () => {
      render(
        <Composer
          surface={surface}
          {...base}
          voiceBlock={{ hasTextDraft: false, hasAttachmentDraft: true }}
        />,
      );
      const line = status();
      expect(line).not.toBeNull();
      // Computed role, not the literal attribute: `<output>` maps to
      // role="status" implicitly, and what matters is what AT resolves.
      expect(screen.getByRole("status")).toBe(line);
      expect(line).toHaveTextContent("COPY_ATTACHMENT");

      const button = mic();
      // Natively disabled — NOT aria-disabled, which stays clickable and lies
      // to assistive tech in a different way (Iris).
      expect(button).toBeDisabled();
      expect(button).not.toHaveAttribute("aria-disabled");
      // The accessible NAME stays the action; the reason is a description.
      expect(button).toHaveAccessibleName("Record voice message");
      expect(button.getAttribute("aria-describedby")).toBe(line?.getAttribute("id"));
    });

    it("renders no status and no description link when recording is available — the inverse half", () => {
      render(<Composer surface={surface} {...base} voiceBlock={NONE} />);
      expect(status()).toBeNull();
      const button = mic();
      expect(button).not.toBeDisabled();
      expect(button).not.toHaveAttribute("aria-describedby");
    });

    // LRM-702 — text-only and text+attachment drafts no longer render an inline
    // hint (silent disable); only the attachment-only draft still explains itself.
    it.each([
      ["attachment only", { hasTextDraft: false, hasAttachmentDraft: true }, "COPY_ATTACHMENT"],
    ])("says the true sentence for %s", (_label, voiceBlock, expected) => {
      render(<Composer surface={surface} {...base} voiceBlock={voiceBlock} />);
      expect(status()).toHaveTextContent(expected);
    });

    // LRM-702 — a text draft disables the mic but renders NO inline hint.
    it("disables the mic for a text draft with no inline hint (LRM-702)", () => {
      render(
        <Composer
          surface={surface}
          {...base}
          voiceBlock={{ hasTextDraft: true, hasAttachmentDraft: false }}
        />,
      );
      expect(status()).toBeNull();
      expect(mic()).toBeDisabled();
    });
  });

  it("a send in flight is explained on its own real prop, not only in the resolver", () => {
    render(<Composer surface="channel" {...base} sending voiceBlock={NONE} />);
    expect(status()).toHaveTextContent("COPY_SENDING");
    expect(mic()).toBeDisabled();
  });

  it("an unsent recording outranks a text draft", () => {
    render(
      <Composer
        surface="channel"
        {...base}
        voiceBlock={{ pendingVoice: true, hasTextDraft: true, hasAttachmentDraft: false }}
      />,
    );
    expect(status()).toHaveTextContent("COPY_PENDING_VOICE");
  });

  it("an attachment upload is never described as a voice upload", () => {
    // The tray's `hasUploading` is a subset of `pending`, so an uploading PDF
    // arrives as `hasAttachmentDraft`. Calling that "uploading your voice
    // message" would be a new false sentence in place of the old one.
    render(
      <Composer
        surface="channel"
        {...base}
        voiceBlock={{ hasTextDraft: false, hasAttachmentDraft: true }}
      />,
    );
    expect(status()).toHaveTextContent("COPY_ATTACHMENT");
    expect(status()).not.toHaveTextContent("COPY_UPLOADING");
  });

  it("omitting voiceBlock leaves the mic enabled with no status — surfaces without the feature are untouched", () => {
    render(<Composer surface="channel" {...base} />);
    expect(status()).toBeNull();
    expect(mic()).not.toBeDisabled();
  });
});
