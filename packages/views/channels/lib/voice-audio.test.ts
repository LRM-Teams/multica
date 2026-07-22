import { describe, expect, it } from "vitest";
import { buildVoiceMessageParts, encodeVoicePCM, VOICE_SAMPLE_RATE } from "./voice-audio";

describe("buildVoiceMessageParts", () => {
  it("keeps the transcript accessible and marks its source modality", () => {
    expect(buildVoiceMessageParts("  hello  ", 1234)).toEqual([
      { type: "text", text: "hello" },
      { type: "voice", duration_ms: 1234 },
    ]);
  });
});

describe("encodeVoicePCM", () => {
  it("encodes clamped signed 16-bit little-endian PCM", () => {
    const pcm = encodeVoicePCM(new Float32Array([-2, -1, 0, 1, 2]), VOICE_SAMPLE_RATE);
    const view = new DataView(pcm);
    expect(Array.from({ length: 5 }, (_, index) => view.getInt16(index * 2, true))).toEqual([
      -32768,
      -32768,
      0,
      32767,
      32767,
    ]);
  });

  it("resamples the output length to 16 kHz", () => {
    const oneSecondAt48K = new Float32Array(48_000);
    expect(encodeVoicePCM(oneSecondAt48K, 48_000).byteLength).toBe(VOICE_SAMPLE_RATE * 2);
  });

  it("rejects an invalid source sample rate", () => {
    expect(() => encodeVoicePCM(new Float32Array([0]), 0)).toThrow("sample rate");
  });
});
