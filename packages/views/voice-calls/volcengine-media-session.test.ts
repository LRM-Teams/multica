// @vitest-environment node
import type { VoiceCallMedia } from "@multica/core/types";
import { afterEach, describe, expect, it, vi } from "vitest";
import {
  VolcengineVoiceMediaSession,
  type VoiceCallRemoteAudioOutput,
  type VoiceCallRTCDriver,
  type VoiceCallRTCEngine,
} from "./volcengine-media-session";

const media: VoiceCallMedia = {
  app_id: "rtc-app",
  room_id: "voice-room",
  user_id: "voice-member-call-1",
  token: "short-lived-room-token",
  expires_at: "2026-07-23T14:00:00Z",
};

function createHarness(overrides: Partial<VoiceCallRTCEngine> = {}) {
  const calls: string[] = [];
  const remoteTrack = { id: "agent-audio-track" } as MediaStreamTrack;
  let callbacks:
    | Parameters<VoiceCallRTCDriver["createEngine"]>[1]
    | undefined;
  const engine: VoiceCallRTCEngine = {
    join: vi.fn(async () => {
      calls.push("join");
    }),
    leave: vi.fn(async () => {
      calls.push("leave");
    }),
    startAudioCapture: vi.fn(async () => {
      calls.push("start");
    }),
    stopAudioCapture: vi.fn(async () => {
      calls.push("stop");
    }),
    publishAudio: vi.fn(async () => {
      calls.push("publish");
    }),
    unpublishAudio: vi.fn(async () => {
      calls.push("unpublish");
    }),
    getRemoteAudioTrack: vi.fn(() => remoteTrack),
    destroy: vi.fn(() => {
      calls.push("destroy");
    }),
    ...overrides,
  };
  const driver: VoiceCallRTCDriver = {
    isSupported: vi.fn(async () => true),
    createEngine: vi.fn((_appId, handlers) => {
      callbacks = handlers;
      calls.push("create");
      return engine;
    }),
  };
  return {
    calls,
    engine,
    remoteTrack,
    driver,
    getCallbacks: () => callbacks,
  };
}

describe("VolcengineVoiceMediaSession", () => {
  afterEach(() => {
    vi.unstubAllGlobals();
  });

  it("rejects insecure origins before loading the RTC SDK", async () => {
    vi.stubGlobal("isSecureContext", false);
    const harness = createHarness();
    const loadDriver = vi.fn(async () => harness.driver);
    const onError = vi.fn();
    const session = new VolcengineVoiceMediaSession(
      { onError },
      loadDriver,
    );

    await expect(session.connect(media)).rejects.toMatchObject({
      code: "insecure_context",
    });

    expect(loadDriver).not.toHaveBeenCalled();
    expect(harness.driver.createEngine).not.toHaveBeenCalled();
    expect(harness.engine.startAudioCapture).not.toHaveBeenCalled();
    expect(onError).toHaveBeenCalledWith(
      expect.objectContaining({ code: "insecure_context" }),
    );
  });

  it("joins, captures, and publishes audio in order", async () => {
    const harness = createHarness();
    const states: string[] = [];
    const session = new VolcengineVoiceMediaSession(
      { onStateChange: (state) => states.push(state) },
      async () => harness.driver,
    );

    await session.connect(media, "microphone-1");

    expect(harness.driver.isSupported).toHaveBeenCalledOnce();
    expect(harness.driver.createEngine).toHaveBeenCalledWith(
      media.app_id,
      expect.any(Object),
    );
    expect(harness.engine.join).toHaveBeenCalledWith(media);
    expect(harness.engine.startAudioCapture).toHaveBeenCalledWith(
      "microphone-1",
    );
    expect(harness.calls).toEqual(["create", "join", "start", "publish"]);
    expect(states).toEqual(["joining", "connected"]);
    expect(session.getState()).toBe("connected");
  });

  it("rejects unsupported browsers before opening an engine or microphone", async () => {
    const harness = createHarness();
    harness.driver.isSupported = vi.fn(async () => false);
    const onError = vi.fn();
    const session = new VolcengineVoiceMediaSession(
      { onError },
      async () => harness.driver,
    );

    await expect(session.connect(media)).rejects.toMatchObject({
      code: "unsupported",
    });

    expect(harness.driver.createEngine).not.toHaveBeenCalled();
    expect(harness.engine.startAudioCapture).not.toHaveBeenCalled();
    expect(session.getState()).toBe("failed");
    expect(onError).toHaveBeenCalledWith(
      expect.objectContaining({ code: "unsupported" }),
    );
  });

  it("leaves and destroys the engine when microphone capture fails", async () => {
    const harness = createHarness({
      startAudioCapture: vi.fn(async () => {
        harness.calls.push("start");
        throw new DOMException("Permission denied", "NotAllowedError");
      }),
    });
    const session = new VolcengineVoiceMediaSession(
      {},
      async () => harness.driver,
    );

    await expect(session.connect(media)).rejects.toMatchObject({
      code: "microphone_unavailable",
    });

    expect(harness.calls).toEqual(["create", "join", "start", "leave", "destroy"]);
    expect(harness.engine.publishAudio).not.toHaveBeenCalled();
    expect(session.getState()).toBe("failed");
  });

  it("stops publishing and releases the microphone when muted", async () => {
    const harness = createHarness();
    const session = new VolcengineVoiceMediaSession(
      {},
      async () => harness.driver,
    );
    await session.connect(media);
    harness.calls.length = 0;

    await session.setMuted(true);
    expect(harness.calls).toEqual(["unpublish", "stop"]);
    expect(session.getState()).toBe("muted");

    harness.calls.length = 0;
    await session.setMuted(false);
    expect(harness.calls).toEqual(["start", "publish"]);
    expect(harness.engine.startAudioCapture).toHaveBeenLastCalledWith(
      undefined,
    );
    expect(session.getState()).toBe("connected");
  });

  it("reuses the selected microphone after mute", async () => {
    const harness = createHarness();
    const session = new VolcengineVoiceMediaSession(
      {},
      async () => harness.driver,
    );
    await session.connect(media, "microphone-1");
    await session.setMuted(true);

    await session.setMuted(false);

    expect(harness.engine.startAudioCapture).toHaveBeenLastCalledWith(
      "microphone-1",
    );
  });

  it("reports partial mute cleanup while keeping outgoing audio blocked", async () => {
    const harness = createHarness({
      unpublishAudio: vi.fn(async () => {
        harness.calls.push("unpublish");
        throw new Error("unpublish failed");
      }),
    });
    const session = new VolcengineVoiceMediaSession(
      {},
      async () => harness.driver,
    );
    await session.connect(media);
    harness.calls.length = 0;

    await expect(session.setMuted(true)).rejects.toMatchObject({
      code: "mute_failed",
    });

    expect(harness.calls).toEqual(["unpublish", "stop"]);
    expect(session.getState()).toBe("muted");
  });

  it("resumes browser-blocked remote audio only after an explicit call", async () => {
    const harness = createHarness();
    const onAutoplayBlocked = vi.fn();
    const output: VoiceCallRemoteAudioOutput = {
      play: vi.fn().mockResolvedValue(undefined),
      stop: vi.fn(),
    };
    const session = new VolcengineVoiceMediaSession(
      { onAutoplayBlocked },
      async () => harness.driver,
      () => output,
    );
    await session.connect(media);

    harness.getCallbacks()?.onAutoplayBlocked("voice-agent-call-1");
    expect(onAutoplayBlocked).toHaveBeenCalledWith("voice-agent-call-1");
    expect(output.play).not.toHaveBeenCalled();

    await session.resumeRemoteAudio("voice-agent-call-1");
    expect(harness.engine.getRemoteAudioTrack).toHaveBeenCalledWith(
      "voice-agent-call-1",
    );
    expect(output.play).toHaveBeenCalledWith(harness.remoteTrack);
  });



  it("ignores decoded audio from a user other than the call-scoped agent", async () => {
    const harness = createHarness();
    const onRemoteAudioStarted = vi.fn();
    const onAutoplayBlocked = vi.fn();
    const session = new VolcengineVoiceMediaSession(
      { onRemoteAudioStarted, onAutoplayBlocked },
      async () => harness.driver,
    );
    await session.connect(media);

    harness.getCallbacks()?.onRemoteAudioStarted("unexpected-remote-user");
    harness.getCallbacks()?.onAutoplayBlocked("unexpected-remote-user");

    await Promise.resolve();
    expect(harness.engine.getRemoteAudioTrack).not.toHaveBeenCalled();
    expect(onRemoteAudioStarted).not.toHaveBeenCalled();
    expect(onAutoplayBlocked).not.toHaveBeenCalled();
  });


  it("releases every acquired resource once on disconnect", async () => {
    const harness = createHarness();
    const session = new VolcengineVoiceMediaSession(
      {},
      async () => harness.driver,
    );
    await session.connect(media);
    harness.calls.length = 0;

    await session.disconnect();
    await session.disconnect();

    expect(harness.calls).toEqual([
      "stop",
      "unpublish",
      "leave",
      "destroy",
    ]);
    expect(session.getState()).toBe("closed");
  });


  it("preserves bounded provider enum codes from rejected SDK calls", async () => {
    const harness = createHarness({
      join: vi.fn(async () => {
        harness.calls.push("join");
        throw Object.assign(new Error("provider detail"), {
          code: "JOIN_ROOM_FAILED",
        });
      }),
    });
    const session = new VolcengineVoiceMediaSession(
      {},
      async () => harness.driver,
    );

    await expect(session.connect(media)).rejects.toMatchObject({
      code: "join_failed",
      providerCode: "JOIN_ROOM_FAILED",
    });
  });

  it("does not expose arbitrary rejection values as provider codes", async () => {
    const harness = createHarness({
      join: vi.fn(async () => {
        harness.calls.push("join");
        throw Object.assign(new Error("provider detail"), {
          code: "raw provider message with spaces",
        });
      }),
    });
    const session = new VolcengineVoiceMediaSession(
      {},
      async () => harness.driver,
    );

    await expect(session.connect(media)).rejects.toMatchObject({
      code: "join_failed",
      providerCode: undefined,
    });
  });

  it("serializes provider cleanup with a simultaneous user disconnect", async () => {
    const harness = createHarness();
    const session = new VolcengineVoiceMediaSession(
      {},
      async () => harness.driver,
    );
    await session.connect(media);

    harness.getCallbacks()?.onFatalError("ROOM_DISMISS");
    await session.disconnect();

    expect(harness.engine.stopAudioCapture).toHaveBeenCalledOnce();
    expect(harness.engine.unpublishAudio).toHaveBeenCalledOnce();
    expect(harness.engine.leave).toHaveBeenCalledOnce();
    expect(harness.engine.destroy).toHaveBeenCalledOnce();
    expect(session.getState()).toBe("closed");
  });
});
