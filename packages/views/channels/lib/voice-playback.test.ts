import { afterEach, describe, expect, it, vi } from "vitest";
import {
  cancelVoicePlayback,
  claimVoiceAutoplay,
  prepareVoicePlayback,
} from "./voice-playback";

afterEach(() => {
  vi.useRealTimers();
});

describe("voice autoplay eligibility", () => {
  it("is consumed by the first new Agent reply, including a text reply", () => {
    vi.useFakeTimers();
    vi.setSystemTime(new Date("2026-07-22T10:00:00.000Z"));
    prepareVoicePlayback("channel:main");

    expect(claimVoiceAutoplay(
      "text-reply",
      "channel:main",
      "2026-07-22T10:00:01.000Z",
    )).toBe(true);
    expect(claimVoiceAutoplay(
      "later-voice-reply",
      "channel:main",
      "2026-07-22T10:00:02.000Z",
    )).toBe(false);
  });

  it("does not consume the scope for an older message", () => {
    vi.useFakeTimers();
    vi.setSystemTime(new Date("2026-07-22T10:00:00.000Z"));
    prepareVoicePlayback("channel:thread");

    expect(claimVoiceAutoplay(
      "old-message",
      "channel:thread",
      "2026-07-22T09:59:00.000Z",
    )).toBe(false);
    expect(claimVoiceAutoplay(
      "new-message",
      "channel:thread",
      "2026-07-22T10:00:01.000Z",
    )).toBe(true);
  });

  it("can cancel a prepared scope after recording fails", () => {
    vi.useFakeTimers();
    vi.setSystemTime(new Date("2026-07-22T10:00:00.000Z"));
    prepareVoicePlayback("channel:cancelled");
    cancelVoicePlayback("channel:cancelled");

    expect(claimVoiceAutoplay(
      "unexpected-reply",
      "channel:cancelled",
      "2026-07-22T10:00:01.000Z",
    )).toBe(false);
  });

  it("prunes old claims in a long-running desktop session", () => {
    vi.useFakeTimers();
    vi.setSystemTime(new Date("2026-07-22T10:00:00.000Z"));
    prepareVoicePlayback("channel:first");
    expect(claimVoiceAutoplay(
      "reused-fixture-id",
      "channel:first",
      "2026-07-22T10:00:01.000Z",
    )).toBe(true);

    vi.setSystemTime(new Date("2026-07-22T10:31:00.000Z"));
    prepareVoicePlayback("channel:second");
    expect(claimVoiceAutoplay(
      "reused-fixture-id",
      "channel:second",
      "2026-07-22T10:31:01.000Z",
    )).toBe(true);
  });
});
