import { readFileSync } from "node:fs";
import { dirname, resolve } from "node:path";
import { fileURLToPath } from "node:url";
import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { beforeEach, describe, expect, it, vi } from "vitest";
import { ComposerPendingVoice, type PendingVoiceState } from "./composer-pending-voice";

vi.mock("../../i18n/use-t", () => ({
  useT: () => ({
    t: (
      selector: (dict: Record<string, unknown>) => unknown,
      vars?: Record<string, string>,
    ) => {
      const dict = {
        composer: {
          voice_unsent: `Voice message not sent (${vars?.duration ?? ""})`,
          voice_unsent_retrying: `Resending voice message (${vars?.duration ?? ""})`,
          voice_unsent_retry: "Retry send",
          voice_unsent_delete: "Delete",
        },
      };
      const value = selector(dict);
      return typeof value === "string" ? value : String(value);
    },
  }),
}));

const pending: PendingVoiceState = {
  targetId: "channel-1",
  channelId: "channel-1",
  durationMs: 7_400,
  attachment: {
    id: "att-1",
    filename: "voice.wav",
    content_type: "audio/wav",
    size_bytes: 1234,
  },
};

function renderTray(overrides?: {
  retrying?: boolean;
  onRetry?: () => void;
  onDelete?: () => void;
}) {
  const onRetry = overrides?.onRetry ?? vi.fn();
  const onDelete = overrides?.onDelete ?? vi.fn();
  const view = render(
    <ComposerPendingVoice
      pending={pending}
      retrying={overrides?.retrying ?? false}
      onRetry={onRetry}
      onDelete={onDelete}
    />,
  );
  const rerender = (retrying: boolean) =>
    view.rerender(
      <ComposerPendingVoice
        pending={pending}
        retrying={retrying}
        onRetry={onRetry}
        onDelete={onDelete}
      />,
    );
  return { ...view, rerender, onRetry, onDelete };
}

const retryBtn = () => screen.getByTestId("composer-pending-voice-retry");
const deleteBtn = () => screen.getByTestId("composer-pending-voice-delete");

describe("ComposerPendingVoice — LRM-1354 (design gate LRM-1352)", () => {
  beforeEach(() => {
    vi.clearAllMocks();
  });

  it("renders nothing without a pending recording", () => {
    render(
      <ComposerPendingVoice
        pending={null}
        retrying={false}
        onRetry={vi.fn()}
        onDelete={vi.fn()}
      />,
    );
    expect(screen.queryByTestId("composer-pending-voice")).toBeNull();
  });

  // AC1 — neither action may carry a native `disabled`: LRM-1213/1169 frozen
  // pattern. A native disabled on the control the user just activated drops
  // focus to <body> in Chromium and never returns it.
  it("AC1: neither Retry nor Delete is ever natively disabled", () => {
    const { rerender } = renderTray({ retrying: false });
    expect((retryBtn() as HTMLButtonElement).disabled).toBe(false);
    expect((deleteBtn() as HTMLButtonElement).disabled).toBe(false);
    rerender(true);
    expect((retryBtn() as HTMLButtonElement).disabled).toBe(false);
    expect((deleteBtn() as HTMLButtonElement).disabled).toBe(false);
  });

  // AC2 — aria-disabled only while retrying; never a literal "false".
  it("AC2: aria-disabled appears only while retrying", () => {
    const { rerender } = renderTray({ retrying: false });
    expect(retryBtn().hasAttribute("aria-disabled")).toBe(false);
    expect(deleteBtn().hasAttribute("aria-disabled")).toBe(false);
    rerender(true);
    expect(retryBtn().getAttribute("aria-disabled")).toBe("true");
    expect(deleteBtn().getAttribute("aria-disabled")).toBe("true");
  });

  // AC3 — the guard is what actually blocks the action now.
  it("AC3: Retry fires once when idle and never while retrying", async () => {
    const user = userEvent.setup();
    const onRetry = vi.fn();
    const { rerender } = renderTray({ retrying: false, onRetry });
    await user.click(retryBtn());
    expect(onRetry).toHaveBeenCalledTimes(1);
    rerender(true);
    await user.click(retryBtn());
    expect(onRetry).toHaveBeenCalledTimes(1);
  });

  // AC4 — explicit abandon is the user's only exit besides success, so it must
  // still work when idle and still be blocked (not removed) while in flight.
  it("AC4: Delete fires once when idle and never while retrying", async () => {
    const user = userEvent.setup();
    const onDelete = vi.fn();
    const { rerender } = renderTray({ retrying: false, onDelete });
    await user.click(deleteBtn());
    expect(onDelete).toHaveBeenCalledTimes(1);
    rerender(true);
    await user.click(deleteBtn());
    expect(onDelete).toHaveBeenCalledTimes(1);
  });

  // AC5 — the node the user activated keeps focus across the idle -> in-flight
  // transition: same node reference, not just the same testid.
  //
  // Honest boundary: jsdom does NOT reproduce Chromium's "focus falls to <body>
  // when the focused element becomes natively disabled", so this case cannot go
  // red on the old code here — the real-browser proof lives on the design gate
  // (LRM-1352, headless Chromium A/C table). AC6 below is the jsdom-observable
  // half of the same defect and DOES go red, because `.focus()` is a no-op on a
  // natively disabled element.
  it("AC5: Retry keeps focus on the very same node when retry goes in flight", () => {
    const { rerender } = renderTray({ retrying: false });
    const before = retryBtn();
    before.focus();
    expect(document.activeElement).toBe(before);
    rerender(true);
    expect(retryBtn()).toBe(before);
    expect(document.activeElement).toBe(before);
  });

  // AC6 — the abandon route must stay reachable by keyboard while in flight.
  it("AC6: Delete is still focusable while a retry is in flight", () => {
    const { rerender } = renderTray({ retrying: false });
    rerender(true);
    const del = deleteBtn();
    del.focus();
    expect(document.activeElement).toBe(del);
  });

  // AC7 — retrying must be announced, not only drawn.
  it("AC7: live region swaps to the retrying copy and sets aria-busy", () => {
    const { rerender } = renderTray({ retrying: false });
    const region = screen.getByTestId("composer-pending-voice-status");
    expect(region.textContent).toBe("Voice message not sent (0:07)");
    expect(region.hasAttribute("aria-busy")).toBe(false);
    rerender(true);
    expect(region.textContent).toBe("Resending voice message (0:07)");
    expect(region.getAttribute("aria-busy")).toBe("true");
  });

  // AC8 — `disabled:` variants stop matching once the native attribute is gone,
  // so the dim state must come from a condition, and only one dim layer.
  it("AC8: pending dim state survives and stays single-level", () => {
    const { rerender } = renderTray({ retrying: false });
    expect(retryBtn().className).not.toContain("opacity-");
    rerender(true);
    for (const btn of [retryBtn(), deleteBtn()]) {
      expect(btn.className).toContain("opacity-50");
      expect(btn.className.match(/opacity-/g)).toHaveLength(1);
    }
  });

  it("AC1/AC8 source: no `disabled={retrying}` and no dead `disabled:` variant", () => {
    const src = readFileSync(
      resolve(dirname(fileURLToPath(import.meta.url)), "composer-pending-voice.tsx"),
      "utf8",
    );
    expect(src).not.toContain("disabled={retrying}");
    expect(src).not.toContain("disabled:opacity-50");
  });

  // AC10 — exactly one new key, in every shipped language, carrying the same
  // interpolation as the copy it replaces.
  it("AC10: voice_unsent_retrying exists in all four locales with {{duration}}", () => {
    const here = dirname(fileURLToPath(import.meta.url));
    for (const lang of ["zh-Hans", "en", "ja", "ko"]) {
      const dict = JSON.parse(
        readFileSync(resolve(here, "../../locales", lang, "channels.json"), "utf8"),
      ) as { composer: Record<string, string> };
      expect(dict.composer.voice_unsent_retrying, lang).toBeTypeOf("string");
      expect(dict.composer.voice_unsent_retrying, lang).toContain("{{duration}}");
      // The three pre-existing keys stay verbatim — this slice is not a copy change.
      expect(dict.composer.voice_unsent, lang).toContain("{{duration}}");
      expect(dict.composer.voice_unsent_retry, lang).toBeTypeOf("string");
      expect(dict.composer.voice_unsent_delete, lang).toBeTypeOf("string");
    }
  });
});
