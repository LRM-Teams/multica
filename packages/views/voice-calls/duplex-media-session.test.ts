// @vitest-environment node
import { describe, expect, it, vi } from "vitest";
import {
  createDuplexMediaSession,
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

  it("stops a microphone stream granted after disconnect", async () => {
    type Listener = (event: Event | MessageEvent<string>) => void;
    let openSocket: (() => void) | undefined;
    class FakeWebSocket {
      readyState = 0;
      readonly send = vi.fn();
      readonly close = vi.fn(() => {
        this.readyState = 3;
      });
      private readonly listeners = new Map<string, Listener>();

      constructor(_url: string) {
        openSocket = () => {
          this.readyState = 1;
          this.listeners.get("open")?.(new Event("open"));
        };
      }

      addEventListener(
        type: "open" | "message" | "error" | "close",
        listener: Listener,
      ) {
        this.listeners.set(type, listener);
      }

      removeEventListener(
        type: "open" | "message" | "error" | "close",
        listener: Listener,
      ) {
        if (this.listeners.get(type) === listener) this.listeners.delete(type);
      }
    }
    class FakeAudioContext {
      state: AudioContextState = "running";
      currentTime = 0;
      readonly resume = vi.fn().mockResolvedValue(undefined);
      readonly close = vi.fn(async () => {
        this.state = "closed";
      });
    }

    let resolveStream: ((stream: MediaStream) => void) | undefined;
    const getUserMedia = vi.fn(() => new Promise<MediaStream>((resolve) => {
      resolveStream = resolve;
    }));
    const track = { stop: vi.fn() } as unknown as MediaStreamTrack;
    const stream = {
      getTracks: () => [track],
    } as unknown as MediaStream;
    const session = createDuplexMediaSession({}, {
      WebSocket: FakeWebSocket as unknown as typeof WebSocket,
      getUserMedia: getUserMedia as typeof navigator.mediaDevices.getUserMedia,
      AudioContext: FakeAudioContext as unknown as typeof AudioContext,
    });

    const connecting = session.connect("wss://voice.example.test/duplex");
    if (!openSocket) throw new Error("Duplex WebSocket was not created");
    openSocket();
    await vi.waitFor(() => {
      expect(getUserMedia).toHaveBeenCalledOnce();
    });

    await session.disconnect();
    resolveStream?.(stream);
    await expect(connecting).resolves.toBeUndefined();

    expect(track.stop).toHaveBeenCalledOnce();
  });

  it("exposes a higher barge-in threshold for speakerphone echo", async () => {
    const {
      DUPLEX_BARGE_IN_RMS,
      DUPLEX_SPEAKERPHONE_BARGE_IN_RMS,
    } = await import("./duplex-media-session");
    expect(DUPLEX_SPEAKERPHONE_BARGE_IN_RMS).toBeGreaterThan(DUPLEX_BARGE_IN_RMS);
  });
});
