import { describe, expect, it } from "vitest";
import { resolveVoiceBlockReason, type VoiceBlockInputs } from "./voice-block-reason";

const NONE: VoiceBlockInputs = {
  voiceUploading: false,
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
    ["uploading", { voiceUploading: true }],
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
      expect(resolve({ voiceUploading: true, ...lower })).toBe("uploading");
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

  it("an attachment upload never resolves to 'uploading' — that reason is voice-only", () => {
    // Iris's boundary (#858): the tray's `hasUploading` is a subset of
    // `pending`, so an uploading PDF arrives here as `hasAttachmentDraft`. It
    // must get the attachment sentence, never "uploading your voice message".
    expect(resolve({ hasAttachmentDraft: true })).toBe("attachment_draft");
    expect(resolve({ hasAttachmentDraft: true })).not.toBe("uploading");
  });

  it("every cause combination resolves to exactly one reason, and only the empty one is null", () => {
    // Exhaustive over all 32 input combinations: guards against a future cause
    // being added without a branch, which would silently return null and put us
    // back to a disabled mic with no explanation.
    const flags = [
      "voiceUploading",
      "pendingVoice",
      "sending",
      "hasTextDraft",
      "hasAttachmentDraft",
    ] as const;
    for (let mask = 0; mask < 1 << flags.length; mask += 1) {
      const inputs = { ...NONE };
      flags.forEach((flag, bit) => {
        inputs[flag] = Boolean(mask & (1 << bit));
      });
      const reason = resolveVoiceBlockReason(inputs);
      if (mask === 0) expect(reason).toBeNull();
      else expect(reason).not.toBeNull();
    }
  });
});
