import { describe, expect, it } from "vitest";
import {
  decodeBase64ToPcmS16le,
  downsampleFloat32To16k,
  encodePcmS16leToBase64,
  parseDuplexServerEvent,
  pcmS16leToFloat32,
} from "./duplex-media-session";

describe("duplex-media-session helpers", () => {
  it("round-trips PCM s16le through base64", () => {
    const samples = Int16Array.from([0, 1000, -1000, 32767, -32768]);
    const encoded = encodePcmS16leToBase64(samples);
    expect(decodeBase64ToPcmS16le(encoded)).toEqual(samples);
  });

  it("downsamples 48 kHz float audio to 16 kHz PCM", () => {
    const input = new Float32Array(480);
    for (let i = 0; i < input.length; i += 1) {
      input[i] = Math.sin((i / 480) * Math.PI * 2);
    }
    const downsampled = downsampleFloat32To16k(input, 48_000);
    expect(downsampled.length).toBe(160);
  });

  it("converts PCM to float samples in [-1, 1]", () => {
    const pcm = Int16Array.from([0, 16384, -16384]);
    expect(Array.from(pcmS16leToFloat32(pcm))).toEqual([
      0,
      0.5,
      -0.5,
    ]);
  });

  it("parses duplex server events by type", () => {
    expect(parseDuplexServerEvent({
      type: "duplex.tool",
      name: "delegate_work_to_multica_agent",
      status: "started",
    })).toMatchObject({
      type: "duplex.tool",
      name: "delegate_work_to_multica_agent",
      status: "started",
    });
    expect(parseDuplexServerEvent(null)).toBeNull();
    expect(parseDuplexServerEvent({ nope: true })).toBeNull();
  });
});
