/**
 * @vitest-environment jsdom
 */
import { afterEach, describe, expect, it, vi } from "vitest";
import { createVoiceCallRingback } from "./voice-call-ringback";

describe("createVoiceCallRingback", () => {
  afterEach(() => {
    vi.useRealTimers();
    vi.unstubAllGlobals();
  });

  it("schedules a 450 Hz ringback cadence and releases audio resources", () => {
    vi.useFakeTimers();
    const frequency = { value: 0 };
    const oscillator = {
      type: "",
      frequency,
      connect: vi.fn(),
      disconnect: vi.fn(),
      start: vi.fn(),
      stop: vi.fn(),
    };
    const gainParam = {
      value: 1,
      cancelScheduledValues: vi.fn(),
      setValueAtTime: vi.fn(),
      linearRampToValueAtTime: vi.fn(),
    };
    const gain = {
      gain: gainParam,
      connect: vi.fn(),
      disconnect: vi.fn(),
    };
    const context = {
      currentTime: 10,
      destination: {},
      createOscillator: vi.fn(() => oscillator),
      createGain: vi.fn(() => gain),
      resume: vi.fn().mockResolvedValue(undefined),
      close: vi.fn().mockResolvedValue(undefined),
    };
    function FakeAudioContext() {
      return context;
    }
    vi.stubGlobal(
      "AudioContext",
      FakeAudioContext as unknown as typeof AudioContext,
    );

    const ringback = createVoiceCallRingback();
    ringback.start();

    expect(frequency.value).toBe(450);
    expect(oscillator.type).toBe("sine");
    expect(oscillator.start).toHaveBeenCalledTimes(1);
    expect(gainParam.linearRampToValueAtTime).toHaveBeenCalledWith(
      0.055,
      10.04,
    );
    expect(gainParam.linearRampToValueAtTime).toHaveBeenCalledWith(
      0,
      11.02,
    );

    vi.advanceTimersByTime(5_000);
    expect(gainParam.cancelScheduledValues).toHaveBeenCalledTimes(2);

    ringback.stop();
    expect(oscillator.stop).toHaveBeenCalledTimes(1);
    expect(oscillator.disconnect).toHaveBeenCalledTimes(1);
    expect(gain.disconnect).toHaveBeenCalledTimes(1);
    expect(context.close).toHaveBeenCalledTimes(1);

    vi.advanceTimersByTime(10_000);
    expect(gainParam.cancelScheduledValues).toHaveBeenCalledTimes(3);
  });
});
