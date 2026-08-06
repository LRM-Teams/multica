// @vitest-environment node
import { describe, expect, it } from "vitest";
import {
  buildRecordedVoiceMessageParts,
  encodeVoicePCM,
  encodeVoiceWAV,
  VOICE_SAMPLE_RATE,
} from "./voice-audio";

describe("buildRecordedVoiceMessageParts", () => {
  it("sends the recording without inventing a browser-owned transcript", () => {
    expect(buildRecordedVoiceMessageParts(1234, {
      id: "recording-1",
      filename: "voice-recording.wav",
      content_type: "audio/wav",
      size_bytes: 48,
    })).toEqual([{
        type: "voice",
        duration_ms: 1234,
        attachment_id: "recording-1",
        filename: "voice-recording.wav",
        content_type: "audio/wav",
        size_bytes: 48,
    }]);
  });
});

describe("encodeVoiceWAV", () => {
  it("wraps 16 kHz mono PCM in a self-describing WAV file", () => {
    const pcm = new Uint8Array([0x00, 0x80, 0xff, 0x7f]).buffer;
    const wav = encodeVoiceWAV(pcm);
    const bytes = new Uint8Array(wav);
    const view = new DataView(wav);
    const ascii = (start: number, length: number) =>
      String.fromCharCode(...bytes.slice(start, start + length));

    expect(ascii(0, 4)).toBe("RIFF");
    expect(ascii(8, 4)).toBe("WAVE");
    expect(ascii(12, 4)).toBe("fmt ");
    expect(view.getUint16(20, true)).toBe(1);
    expect(view.getUint16(22, true)).toBe(1);
    expect(view.getUint32(24, true)).toBe(VOICE_SAMPLE_RATE);
    expect(view.getUint16(34, true)).toBe(16);
    expect(ascii(36, 4)).toBe("data");
    expect(view.getUint32(40, true)).toBe(pcm.byteLength);
    expect(Array.from(bytes.slice(44))).toEqual([0x00, 0x80, 0xff, 0x7f]);
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
