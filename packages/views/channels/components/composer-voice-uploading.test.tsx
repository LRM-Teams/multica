import type { CSSProperties, ReactNode } from "react";
import { fireEvent, render, screen, waitFor } from "@testing-library/react";
import { beforeEach, describe, expect, it, vi } from "vitest";
import { Composer } from "./composer";

/**
 * #858 — the `uploading` cause, driven through the REAL VoiceInputButton.
 *
 * The resolver unit tests prove the mapping and the button reports its own busy
 * state, but neither shows that a genuine upload reaches the shell and renders
 * the sentence. Every trap this feature hit today was in wiring, not in a pure
 * function ({items} vs messages, a mock dropping `header`, a reject aimed at the
 * wrong api method) — a green resolver plus wrong wiring is a confidently wrong
 * green. So this file drives capture with MediaRecorder/getUserMedia doubles and
 * asserts the shell, exactly where the user would see it.
 *
 * Deliberately its own file: these doubles are module-wide, and composer.test.tsx
 * renders the same button without ever activating it. Keeping them apart means a
 * capture double can't quietly change what those tests exercise.
 */

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
        voice_blocked_starting: string;
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
        voice_blocked_starting: "COPY_STARTING",
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

vi.mock("../lib/voice-capture", () => ({ voiceCaptureUnavailableReason: () => undefined }));
vi.mock("../lib/voice-playback", () => ({
  prepareVoicePlayback: vi.fn(),
  cancelVoicePlayback: vi.fn(),
}));
vi.mock("../lib/voice-audio", async (importOriginal) => ({
  ...(await importOriginal<typeof import("../lib/voice-audio")>()),
  downmixAudioBuffer: () => new Float32Array([0.1, 0.2]),
  encodeVoicePCM: () => new ArrayBuffer(8),
}));

// The upload is held open on purpose: `state` sits at "uploading" for exactly as
// long as this promise is unresolved, which is the window under test.
const delivery = vi.hoisted(() => ({ resolve: undefined as undefined | (() => void) }));
// Held open so the machine sits in "starting" — the window where the old
// `busy` boolean wrongly claimed an upload was already in flight.
const media = vi.hoisted(() => ({ resolve: undefined as undefined | (() => void) }));
vi.mock("../lib/voice-recording-delivery", () => ({
  deliverVoiceRecording: vi.fn(
    () =>
      new Promise((resolve) => {
        delivery.resolve = () =>
          resolve({ attachment: { id: "att-1", url: "u", filename: "v.wav" } });
      }),
  ),
}));

class FakeMediaRecorder {
  static isTypeSupported = () => true;
  state: "inactive" | "recording" = "inactive";
  ondataavailable: ((e: { data: Blob }) => void) | null = null;
  onstop: (() => void) | null = null;
  onerror: (() => void) | null = null;
  mimeType = "audio/webm";
  start() {
    this.state = "recording";
  }
  stop() {
    this.state = "inactive";
    this.ondataavailable?.({ data: new Blob(["x"]) });
    this.onstop?.();
  }
}

describe("Composer — the uploading cause, driven through a real capture (#858)", () => {
  const base = {
    editor: <div data-testid="composer-editor">Editor</div>,
    sendLabel: "Send",
    onSend: vi.fn(),
    isMobile: false,
    sendDisabled: false,
    voiceChannelId: "chan-1",
    voicePlaybackScope: "scope-1",
  };

  beforeEach(() => {
    delivery.resolve = undefined;
    vi.stubGlobal("MediaRecorder", FakeMediaRecorder);
    vi.stubGlobal("AudioContext", class {
      decodeAudioData = async () => ({ sampleRate: 16_000 });
      close = async () => undefined;
    });
    media.resolve = undefined;
    vi.stubGlobal("navigator", {
      ...globalThis.navigator,
      mediaDevices: {
        getUserMedia: () =>
          new Promise((resolve) => {
            media.resolve = () => resolve({ getTracks: () => [] });
          }),
      },
    });
  });

  function mic() {
    return screen.getByRole("button", {
      name: /Record voice message|Stop recording|Uploading voice message/,
    });
  }
  function status() {
    return document.querySelector('[data-slot="composer-voice-block-status"]');
  }

  it("renders the uploading sentence while the recording actually uploads, with the mic natively disabled and described by it", async () => {
    render(
      <Composer
        surface="channel"
        {...base}
        onVoiceSend={() => true}
        voiceBlock={{ hasTextDraft: false, hasAttachmentDraft: false }}
      />,
    );

    // Nothing blocks recording yet — the control that makes the assertion below
    // mean something. Without it, "the uploading copy is present" would also
    // pass on a shell that renders it unconditionally.
    expect(status()).toBeNull();

    fireEvent.click(mic()); // start capture — getUserMedia is held pending
    media.resolve?.();
    await waitFor(() => expect(mic()).toHaveAccessibleName("Stop recording"));
    fireEvent.click(mic()); // stop → enters "uploading" and stays there

    await waitFor(() => expect(status()).not.toBeNull());
    const line = status();
    expect(line).toHaveTextContent("COPY_UPLOADING");
    // `<output>` carries role=status implicitly; assert what AT resolves.
    expect(screen.getByRole("status")).toBe(line);

    // NOTE: during its OWN upload the button announces "Uploading voice
    // message" — pre-existing behaviour and correct, since the name describes
    // what the control is doing. The "name stays 'Record voice message'" rule
    // applies to the mic being blocked by OTHER causes (asserted in
    // composer.test.tsx), not to this one.
    const button = screen.getByRole("button", { name: "Uploading voice message" });
    expect(button).toBeDisabled();
    expect(button).not.toHaveAttribute("aria-disabled");
    expect(button.getAttribute("aria-describedby")).toBe(line?.getAttribute("id"));

    // …and it clears once the upload finishes, rather than sticking.
    delivery.resolve?.();
    await waitFor(() => expect(status()).toBeNull());
  });

  it("an attachment upload keeps the attachment sentence — never projected as a voice upload", async () => {
    // The tray's `hasUploading` is a subset of `pending`, so an uploading PDF
    // arrives as `hasAttachmentDraft`. It must not borrow the voice sentence.
    render(
      <Composer
        surface="channel"
        {...base}
        onVoiceSend={() => true}
        voiceBlock={{ hasTextDraft: false, hasAttachmentDraft: true }}
      />,
    );
    expect(status()).toHaveTextContent("COPY_ATTACHMENT");
    expect(status()).not.toHaveTextContent("COPY_UPLOADING");
  });

  it("says it is PREPARING while acquiring the mic, and only says uploading after the recording is handed off", async () => {
    // The gap Iris caught in review: `busy = starting || uploading` reported one
    // boolean, so this window announced "uploading your voice message" while
    // getUserMedia was still pending and nothing had been uploaded. Same defect
    // as the original shared sentence, one state earlier.
    render(
      <Composer
        surface="channel"
        {...base}
        onVoiceSend={() => true}
        voiceBlock={{ hasTextDraft: false, hasAttachmentDraft: false }}
      />,
    );
    expect(status()).toBeNull();

    fireEvent.click(mic()); // getUserMedia deliberately left pending

    await waitFor(() => expect(status()).not.toBeNull());
    const starting = status();
    expect(starting).toHaveTextContent("COPY_STARTING");
    // The whole point: it must NOT claim an upload yet.
    expect(starting).not.toHaveTextContent("COPY_UPLOADING");
    expect(screen.getByRole("status")).toBe(starting);
    const preparing = mic();
    expect(preparing).toBeDisabled();
    expect(preparing).not.toHaveAttribute("aria-disabled");
    expect(preparing.getAttribute("aria-describedby")).toBe(starting?.getAttribute("id"));

    // Let the mic open, record, then stop → NOW it is genuinely uploading.
    media.resolve?.();
    await waitFor(() => expect(mic()).toHaveAccessibleName("Stop recording"));
    fireEvent.click(mic());

    await waitFor(() => expect(status()).toHaveTextContent("COPY_UPLOADING"));
    expect(status()).not.toHaveTextContent("COPY_STARTING");

    delivery.resolve?.();
    await waitFor(() => expect(status()).toBeNull());
  });
});
