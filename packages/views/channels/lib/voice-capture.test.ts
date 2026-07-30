// @vitest-environment node
import { describe, expect, it } from "vitest";
import { voiceCaptureUnavailableReason } from "./voice-capture";

const supported = {
  secureContext: true,
  hasGetUserMedia: true,
  hasMediaRecorder: true,
  hasAudioContext: true,
};

describe("voiceCaptureUnavailableReason", () => {
  it("identifies HTTP origins separately from browser capability failures", () => {
    expect(voiceCaptureUnavailableReason({ ...supported, secureContext: false }))
      .toBe("insecure-context");
  });

  it("accepts a secure origin with the complete recording API set", () => {
    expect(voiceCaptureUnavailableReason(supported)).toBeNull();
  });

  it("reports a missing recording API as unsupported", () => {
    expect(voiceCaptureUnavailableReason({ ...supported, hasMediaRecorder: false }))
      .toBe("unsupported");
  });
});
