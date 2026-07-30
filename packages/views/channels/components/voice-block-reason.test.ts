// @vitest-environment node
import { describe, expect, it } from "vitest";
import { resolveVoiceBlockReason, type VoiceBlockInputs } from "./voice-block-reason";

const NONE: VoiceBlockInputs = {
  capturePhase: "idle",
  pendingVoice: false,
  sending: false,
  hasTextDraft: false,
  hasAttachmentDraft: false,
};

const resolve = (over: Partial<VoiceBlockInputs> = {}) =>
  resolveVoiceBlockReason({ ...NONE, ...over });

describe("resolveVoiceBlockReason (#858)", () => {
  it("returns null when nothing blocks recording — the inverse half", () => {
    // Without this, a resolver that returned a reason unconditionally would
    // satisfy every other case here: "the right sentence appears" says nothing
    // about "no sentence appears when it shouldn't".
    expect(resolve()).toBeNull();
  });

  it.each([
    ["starting", { capturePhase: "starting" }],
    ["uploading", { capturePhase: "uploading" }],
    ["pending_voice", { pendingVoice: true }],
    ["sending", { sending: true }],
    ["text_draft", { hasTextDraft: true }],
    ["attachment_draft", { hasAttachmentDraft: true }],
  ] as const)("resolves %s when it is the only cause", (expected, only) => {
    expect(resolve(only)).toBe(expected);
  });

  it("resolves text_and_attachment_draft when BOTH drafts exist — never one of them", () => {
    // The whole reason this state has its own sentence: naming only one cause
    // leaves the mic disabled after the user complies.
    expect(resolve({ hasTextDraft: true, hasAttachmentDraft: true })).toBe(
      "text_and_attachment_draft",
    );
  });

  describe("priority — first match wins, verified pairwise against every lower cause", () => {
    it.each([
      ["pending_voice", { pendingVoice: true }],
      ["sending", { sending: true }],
      ["text draft", { hasTextDraft: true }],
      ["attachment draft", { hasAttachmentDraft: true }],
    ] as const)("uploading outranks %s", (_label, lower) => {
      expect(resolve({ capturePhase: "uploading", ...lower })).toBe("uploading");
    });

    it.each([
      ["pending_voice", { pendingVoice: true }],
      ["sending", { sending: true }],
      ["text draft", { hasTextDraft: true }],
      ["attachment draft", { hasAttachmentDraft: true }],
    ] as const)("starting outranks %s", (_label, lower) => {
      expect(resolve({ capturePhase: "starting", ...lower })).toBe("starting");
    });

    it("starting and uploading are distinct — acquiring the mic is not an upload", () => {
      // The bug this replaced: one `busy` boolean covered both, so the shell
      // said "uploading your voice message" while getUserMedia was still
      // pending and nothing had been uploaded.
      expect(resolve({ capturePhase: "starting" })).toBe("starting");
      expect(resolve({ capturePhase: "starting" })).not.toBe("uploading");
      expect(resolve({ capturePhase: "uploading" })).toBe("uploading");
    });

    it("recording is not a blocked state — it maps to idle and blocks nothing", () => {
      // While recording, the control is the STOP button; there is nothing to
      // explain because nothing is unavailable.
      expect(resolve({ capturePhase: "idle" })).toBeNull();
    });

    it.each([
      ["sending", { sending: true }],
      ["text draft", { hasTextDraft: true }],
      ["attachment draft", { hasAttachmentDraft: true }],
    ] as const)("pending_voice outranks %s", (_label, lower) => {
      expect(resolve({ pendingVoice: true, ...lower })).toBe("pending_voice");
    });

    it.each([
      ["text draft", { hasTextDraft: true }],
      ["attachment draft", { hasAttachmentDraft: true }],
      ["both drafts", { hasTextDraft: true, hasAttachmentDraft: true }],
    ] as const)("sending outranks %s", (_label, lower) => {
      expect(resolve({ sending: true, ...lower })).toBe("sending");
    });
  });

  it("an attachment upload never resolves to 'uploading' or 'starting' — those are voice-only", () => {
    // Iris's boundary (#858): the tray's `hasUploading` is a subset of
    // `pending`, so an uploading PDF arrives here as `hasAttachmentDraft`. It
    // must get the attachment sentence, never "uploading your voice message".
    expect(resolve({ hasAttachmentDraft: true })).toBe("attachment_draft");
    expect(resolve({ hasAttachmentDraft: true })).not.toBe("uploading");
    expect(resolve({ hasAttachmentDraft: true })).not.toBe("starting");
  });

  it("every cause combination resolves to exactly one reason, and only the empty one is null", () => {
    // Exhaustive over all 32 input combinations: guards against a future cause
    // being added without a branch, which would silently return null and put us
    // back to a disabled mic with no explanation.
    const flags = [
      "pendingVoice",
      "sending",
      "hasTextDraft",
      "hasAttachmentDraft",
    ] as const;
    const phases = ["idle", "starting", "uploading"] as const;
    for (const phase of phases) {
      for (let mask = 0; mask < 1 << flags.length; mask += 1) {
        const inputs: VoiceBlockInputs = { ...NONE, capturePhase: phase };
        flags.forEach((flag, bit) => {
          inputs[flag] = Boolean(mask & (1 << bit));
        });
        const reason = resolveVoiceBlockReason(inputs);
        if (phase === "idle" && mask === 0) expect(reason).toBeNull();
        else expect(reason).not.toBeNull();
      }
    }
  });
});
