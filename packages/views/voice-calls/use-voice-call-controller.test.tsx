/**
 * @vitest-environment jsdom
 */
import { setApiInstance } from "@multica/core/api";
import { ApiError, type ApiClient } from "@multica/core/api/client";
import { voiceCallKeys } from "@multica/core/voice-calls";
import type {
  CreateVoiceCallResponse,
  GetVoiceCallResponse,
} from "@multica/core/types";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { act, renderHook } from "@testing-library/react";
import type { ReactNode } from "react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import {
  VoiceCallMediaError,
  type VoiceCallMediaEvents,
} from "./volcengine-media-session";
import {
  useVoiceCallController,
  type VoiceCallMediaSession,
} from "./use-voice-call-controller";
import type {
  DuplexMediaSession,
  DuplexMediaSessionEvents,
} from "./duplex-media-session";

vi.mock("./voice-call-mic-preflight", () => ({
  preflightMicrophoneAccess: vi.fn().mockResolvedValue(undefined),
}));

import { preflightMicrophoneAccess } from "./voice-call-mic-preflight";

const createdCall: CreateVoiceCallResponse = {
  call: {
    id: "call-1",
    channel_id: "channel-1",
    agent_id: "agent-1",
    status: "starting",
    started_at: "2026-07-23T10:00:00Z",
    connected_at: null,
    ended_at: null,
    end_reason: "",
    error_code: "",
    input_audio_ms: 0,
    output_audio_ms: 0,
    updated_at: "2026-07-23T10:00:01Z",
  },
  media: {
    app_id: "rtc-app",
    room_id: "room-1",
    user_id: "voice-member-1",
    token: "short-lived-secret",
    expires_at: "2026-07-23T10:05:00Z",
  },
};

const endedCall: GetVoiceCallResponse = {
  call: {
    ...createdCall.call,
    status: "ended",
    ended_at: "2026-07-23T10:02:00Z",
    end_reason: "user_hangup",
  },
};

const activeCall: GetVoiceCallResponse = {
  call: {
    ...createdCall.call,
    status: "active",
    connected_at: "2026-07-23T10:00:02Z",
  },
};

function wrapper(queryClient: QueryClient) {
  return function QueryWrapper({ children }: { children: ReactNode }) {
    return (
      <QueryClientProvider client={queryClient}>
        {children}
      </QueryClientProvider>
    );
  };
}

function createFakeMediaSession(
  connectImpl?: (
    events: VoiceCallMediaEvents,
  ) => Promise<void>,
) {
  let events: VoiceCallMediaEvents = {};
  const session: VoiceCallMediaSession = {
    connect: vi.fn(async () => {
      events.onStateChange?.("joining");
      if (connectImpl) {
        await connectImpl(events);
      }
      events.onStateChange?.("connected");
    }),
    setMuted: vi.fn(async (muted: boolean) => {
      events.onStateChange?.(muted ? "muted" : "connected");
    }),
    resumeRemoteAudio: vi.fn().mockResolvedValue(undefined),
    disconnect: vi.fn().mockResolvedValue(undefined),
  };
  const factory = vi.fn((nextEvents: VoiceCallMediaEvents) => {
    events = nextEvents;
    return session;
  });
  return {
    events: () => events,
    factory,
    session,
  };
}

function createFakeDuplexSession(
  connectImpl?: (
    events: DuplexMediaSessionEvents,
  ) => Promise<void>,
) {
  let events: DuplexMediaSessionEvents = {};
  const session: DuplexMediaSession = {
    connect: vi.fn(async () => {
      if (connectImpl) {
        await connectImpl(events);
      } else {
        events.onReady?.("duplex-session-1");
        events.onPlaybackStarted?.();
      }
    }),
    setMuted: vi.fn(),
    setSpeakerphone: vi.fn(),
    interrupt: vi.fn(),
    disconnect: vi.fn().mockResolvedValue(undefined),
  };
  const factory = vi.fn((nextEvents: DuplexMediaSessionEvents) => {
    events = nextEvents;
    return session;
  });
  return {
    events: () => events,
    factory,
    session,
  };
}

function createFakeRingback() {
  const ringback = {
    start: vi.fn(),
    stop: vi.fn(),
  };
  return {
    factory: vi.fn(() => ringback),
    ringback,
  };
}

describe("useVoiceCallController", () => {
  let queryClient: QueryClient;
  let createVoiceCall: ReturnType<typeof vi.fn>;
  let connectVoiceCall: ReturnType<typeof vi.fn>;
  let answerVoiceCall: ReturnType<typeof vi.fn>;
  let stopVoiceCall: ReturnType<typeof vi.fn>;
  let getVoiceCall: ReturnType<typeof vi.fn>;
  let startVoiceCallDuplex: ReturnType<typeof vi.fn>;

  beforeEach(() => {
    queryClient = new QueryClient({
      defaultOptions: {
        queries: { retry: false },
        mutations: { retry: false },
      },
    });
    vi.mocked(preflightMicrophoneAccess).mockClear();
    vi.mocked(preflightMicrophoneAccess).mockResolvedValue(undefined);
    createVoiceCall = vi.fn().mockResolvedValue(createdCall);
    connectVoiceCall = vi.fn().mockResolvedValue({
      call: { ...createdCall.call, status: "connecting" },
    });
    answerVoiceCall = vi.fn().mockResolvedValue({
      call: {
        ...createdCall.call,
        status: "active",
        connected_at: "2026-08-01T00:12:00Z",
      },
    });
    stopVoiceCall = vi.fn().mockResolvedValue(endedCall);
    getVoiceCall = vi.fn().mockResolvedValue({ call: createdCall.call });
    startVoiceCallDuplex = vi.fn().mockRejectedValue(
      new ApiError(
        "duplex not configured",
        503,
        "Service Unavailable",
        { code: "duplex_not_configured" },
      ),
    );
    setApiInstance({
      createVoiceCall,
      connectVoiceCall,
      answerVoiceCall,
      stopVoiceCall,
      getVoiceCall,
      startVoiceCallDuplex,
      getBaseUrl: () => "https://api.example.test",
    } as unknown as ApiClient);
  });

  afterEach(() => {
    queryClient.clear();
    vi.restoreAllMocks();
  });

  it("keeps ringback and connecting state until expected remote audio is audible", async () => {
    let resolveCreate:
      | ((created: CreateVoiceCallResponse) => void)
      | undefined;
    createVoiceCall.mockReturnValue(new Promise<CreateVoiceCallResponse>(
      (resolve) => {
        resolveCreate = resolve;
      },
    ));
    const media = createFakeMediaSession();
    const ringback = createFakeRingback();
    const { result } = renderHook(
      () => useVoiceCallController("workspace-1", {
        mediaSessionFactory: media.factory,
        ringbackFactory: ringback.factory,
      }),
      { wrapper: wrapper(queryClient) },
    );

    let startPromise: Promise<string>;
    act(() => {
      startPromise = result.current.start({
        channel_id: "channel-1",
        agent_id: "agent-1",
      });
    });

    expect(ringback.factory).toHaveBeenCalledTimes(1);
    expect(ringback.ringback.start).toHaveBeenCalledTimes(1);
    expect(ringback.ringback.stop).not.toHaveBeenCalled();

    resolveCreate?.(createdCall);
    await act(async () => {
      await startPromise;
    });

    expect(result.current.phase).toBe("joining");
    expect(ringback.ringback.stop).not.toHaveBeenCalled();

    act(() => {
      queryClient.setQueryData<GetVoiceCallResponse>(
        voiceCallKeys.detail("workspace-1", "call-1"),
        activeCall,
      );
    });

    await act(async () => {
      await Promise.resolve();
    });
    expect(result.current.phase).toBe("joining");
    expect(ringback.ringback.stop).not.toHaveBeenCalled();

    act(() => {
      media.events().onRemoteAudioStarted?.("voice-agent-1");
    });

    expect(result.current.phase).toBe("connected");
    expect(ringback.ringback.stop).toHaveBeenCalledTimes(1);
  });

  it("does not treat a late local RTC connection event as an agent answer", async () => {
    const media = createFakeMediaSession();
    const ringback = createFakeRingback();
    const { result } = renderHook(
      () => useVoiceCallController("workspace-1", {
        mediaSessionFactory: media.factory,
        ringbackFactory: ringback.factory,
      }),
      { wrapper: wrapper(queryClient) },
    );

    await act(async () => {
      await result.current.start({
        channel_id: "channel-1",
        agent_id: "agent-1",
      });
    });

    act(() => {
      media.events().onStateChange?.("connected");
    });

    expect(result.current.phase).toBe("joining");
    expect(ringback.ringback.stop).not.toHaveBeenCalled();
  });

  it("fails and cleans up when the provider never becomes active", async () => {
    vi.useFakeTimers();
    try {
      const media = createFakeMediaSession();
      const ringback = createFakeRingback();
      const { result } = renderHook(
        () => useVoiceCallController("workspace-1", {
          mediaSessionFactory: media.factory,
          ringbackFactory: ringback.factory,
          activationTimeoutMs: 1_000,
        }),
        { wrapper: wrapper(queryClient) },
      );

      await act(async () => {
        await result.current.start({
          channel_id: "channel-1",
          agent_id: "agent-1",
        });
      });
      expect(result.current.phase).toBe("joining");

      await act(async () => {
        await vi.advanceTimersByTimeAsync(1_000);
      });

      expect(ringback.ringback.stop).toHaveBeenCalled();
      expect(media.session.disconnect).toHaveBeenCalledTimes(1);
      expect(stopVoiceCall).toHaveBeenCalledWith("workspace-1", "call-1");
      expect(result.current.phase).toBe("failed");
      expect(result.current.error).toMatchObject({
        source: "server",
        code: "provider_activation_timeout",
      });
    } finally {
      vi.useRealTimers();
    }
  });

  it("times out when the provider task is active but no audio becomes audible", async () => {
    vi.useFakeTimers();
    try {
      const media = createFakeMediaSession();
      const ringback = createFakeRingback();
      const { result } = renderHook(
        () => useVoiceCallController("workspace-1", {
          mediaSessionFactory: media.factory,
          ringbackFactory: ringback.factory,
          activationTimeoutMs: 1_000,
        }),
        { wrapper: wrapper(queryClient) },
      );

      await act(async () => {
        await result.current.start({
          channel_id: "channel-1",
          agent_id: "agent-1",
        });
      });
      getVoiceCall.mockResolvedValue(activeCall);

      await act(async () => {
        await vi.advanceTimersByTimeAsync(1_000);
      });

      expect(result.current.phase).toBe("failed");
      expect(result.current.error).toMatchObject({
        source: "server",
        code: "provider_activation_timeout",
      });
      expect(media.session.disconnect).toHaveBeenCalledOnce();
      expect(stopVoiceCall).toHaveBeenCalledWith("workspace-1", "call-1");
      expect(ringback.ringback.stop).toHaveBeenCalled();
    } finally {
      vi.useRealTimers();
    }
  });

  it("accepts remote agent audio as the answer when server callbacks are unavailable", async () => {
    vi.useFakeTimers();
    try {
      const media = createFakeMediaSession();
      const ringback = createFakeRingback();
      const { result } = renderHook(
        () => useVoiceCallController("workspace-1", {
          mediaSessionFactory: media.factory,
          ringbackFactory: ringback.factory,
          activationTimeoutMs: 1_000,
        }),
        { wrapper: wrapper(queryClient) },
      );

      await act(async () => {
        await result.current.start({
          channel_id: "channel-1",
          agent_id: "agent-1",
        });
      });
      expect(result.current.phase).toBe("joining");

      act(() => {
        media.events().onRemoteAudioStarted?.("voice-agent-1");
      });

      expect(result.current.phase).toBe("connected");
      expect(ringback.ringback.stop).toHaveBeenCalled();
      await act(async () => {
        await Promise.resolve();
      });
      expect(answerVoiceCall).toHaveBeenCalledWith("workspace-1", "call-1");

      await act(async () => {
        await vi.advanceTimersByTimeAsync(1_000);
      });

      expect(result.current.error).toBeNull();
      expect(media.session.disconnect).not.toHaveBeenCalled();
      expect(stopVoiceCall).not.toHaveBeenCalled();
    } finally {
      vi.useRealTimers();
    }
  });


  it("retries /answer until connected_at syncs without hanging up audible media", async () => {
    vi.useFakeTimers();
    try {
      answerVoiceCall
        .mockRejectedValueOnce(new Error("answer temporarily unavailable"))
        .mockResolvedValueOnce({
          call: {
            ...createdCall.call,
            status: "active",
            connected_at: "2026-08-01T00:12:00Z",
          },
        });
      const media = createFakeMediaSession();
      const { result } = renderHook(
        () => useVoiceCallController("workspace-1", {
          mediaSessionFactory: media.factory,
          activationTimeoutMs: 30_000,
        }),
        { wrapper: wrapper(queryClient) },
      );

      await act(async () => {
        await result.current.start({
          channel_id: "channel-1",
          agent_id: "agent-1",
        });
      });
      act(() => {
        media.events().onRemoteAudioStarted?.("voice-agent-1");
      });
      expect(result.current.phase).toBe("connected");
      await act(async () => {
        await Promise.resolve();
      });
      expect(answerVoiceCall).toHaveBeenCalledTimes(1);

      await act(async () => {
        await vi.advanceTimersByTimeAsync(1_500);
        await Promise.resolve();
      });
      expect(answerVoiceCall).toHaveBeenCalledTimes(2);
      expect(result.current.phase).toBe("connected");
      expect(media.session.disconnect).not.toHaveBeenCalled();
      expect(stopVoiceCall).not.toHaveBeenCalled();
    } finally {
      vi.useRealTimers();
    }
  });


  it("creates the server call and connects media without exposing credentials", async () => {
    const media = createFakeMediaSession();
    const { result } = renderHook(
      () => useVoiceCallController("workspace-1", {
        mediaSessionFactory: media.factory,
      }),
      { wrapper: wrapper(queryClient) },
    );

    let callId = "";
    await act(async () => {
      callId = await result.current.start({
        channel_id: "channel-1",
        agent_id: "agent-1",
      }, "microphone-1");
    });

    expect(callId).toBe("call-1");
    expect(createVoiceCall).toHaveBeenCalledWith(
      "workspace-1",
      {
        channel_id: "channel-1",
        agent_id: "agent-1",
      },
    );
    expect(media.session.connect).toHaveBeenCalledWith(
      createdCall.media,
      "microphone-1",
    );
    expect(result.current.phase).toBe("joining");
    expect(result.current.callId).toBe("call-1");
    expect(JSON.stringify(result.current)).not.toContain("short-lived-secret");
  });

  it("stops the created call once when media startup fails", async () => {
    const failure = new VoiceCallMediaError(
      "microphone_unavailable",
      "Microphone permission denied",
    );
    const media = createFakeMediaSession(async (events) => {
      events.onError?.(failure);
      throw failure;
    });
    const ringback = createFakeRingback();
    const { result } = renderHook(
      () => useVoiceCallController("workspace-1", {
        mediaSessionFactory: media.factory,
        ringbackFactory: ringback.factory,
      }),
      { wrapper: wrapper(queryClient) },
    );

    await act(async () => {
      await expect(result.current.start({
        channel_id: "channel-1",
        agent_id: "agent-1",
      })).rejects.toBe(failure);
    });

    expect(stopVoiceCall).toHaveBeenCalledTimes(1);
    expect(stopVoiceCall).toHaveBeenCalledWith("workspace-1", "call-1");
    expect(media.session.disconnect).toHaveBeenCalled();
    expect(ringback.ringback.stop).toHaveBeenCalledTimes(1);
    expect(result.current.phase).toBe("failed");
    expect(result.current.error).toMatchObject({
      source: "media",
      code: "microphone_unavailable",
    });
  });

  it("keeps the call available for a retry when failure cleanup cannot stop it", async () => {
    const failure = new VoiceCallMediaError(
      "join_failed",
      "RTC join failed",
    );
    stopVoiceCall
      .mockRejectedValueOnce(new Error("stop endpoint unavailable"))
      .mockResolvedValueOnce(endedCall);
    const media = createFakeMediaSession(async (events) => {
      events.onError?.(failure);
      throw failure;
    });
    const { result } = renderHook(
      () => useVoiceCallController("workspace-1", {
        mediaSessionFactory: media.factory,
      }),
      { wrapper: wrapper(queryClient) },
    );

    await act(async () => {
      await expect(result.current.start({
        channel_id: "channel-1",
        agent_id: "agent-1",
      })).rejects.toBe(failure);
    });
    expect(result.current.error).toMatchObject({
      source: "stop",
      code: "stop_failed",
    });

    await act(async () => {
      await result.current.hangUp();
    });

    expect(stopVoiceCall).toHaveBeenCalledTimes(2);
    expect(result.current.phase).toBe("ended");
  });


  it("hangs up locally and on the server", async () => {
    const media = createFakeMediaSession();
    const { result } = renderHook(
      () => useVoiceCallController("workspace-1", {
        mediaSessionFactory: media.factory,
      }),
      { wrapper: wrapper(queryClient) },
    );
    await act(async () => {
      await result.current.start({
        channel_id: "channel-1",
        agent_id: "agent-1",
      });
      await result.current.hangUp();
    });

    expect(media.session.disconnect).toHaveBeenCalledTimes(1);
    expect(stopVoiceCall).toHaveBeenCalledWith("workspace-1", "call-1");
    expect(result.current.phase).toBe("ended");
  });

  it("cancels a start that is waiting for the create response", async () => {
    let resolveCreate:
      | ((created: CreateVoiceCallResponse) => void)
      | undefined;
    createVoiceCall.mockReturnValue(new Promise<CreateVoiceCallResponse>(
      (resolve) => {
        resolveCreate = resolve;
      },
    ));
    const media = createFakeMediaSession();
    const { result } = renderHook(
      () => useVoiceCallController("workspace-1", {
        mediaSessionFactory: media.factory,
      }),
      { wrapper: wrapper(queryClient) },
    );

    let startPromise: Promise<string>;
    await act(async () => {
      startPromise = result.current.start({
        channel_id: "channel-1",
        agent_id: "agent-1",
      });
    });
    let hangUpPromise: Promise<void>;
    act(() => {
      hangUpPromise = result.current.hangUp();
    });
    resolveCreate?.(createdCall);

    await act(async () => {
      await hangUpPromise;
    });
    await expect(startPromise!).rejects.toMatchObject({ code: "cancelled" });
    expect(media.factory).not.toHaveBeenCalled();
    expect(stopVoiceCall).toHaveBeenCalledWith("workspace-1", "call-1");
    expect(result.current.phase).toBe("ended");
  });

  it("allows retry when cancelling a pending start cannot stop the server call", async () => {
    let resolveCreate:
      | ((created: CreateVoiceCallResponse) => void)
      | undefined;
    createVoiceCall.mockReturnValue(new Promise<CreateVoiceCallResponse>(
      (resolve) => {
        resolveCreate = resolve;
      },
    ));
    stopVoiceCall
      .mockRejectedValueOnce(new Error("stop endpoint unavailable"))
      .mockResolvedValueOnce(endedCall);
    const media = createFakeMediaSession();
    const { result } = renderHook(
      () => useVoiceCallController("workspace-1", {
        mediaSessionFactory: media.factory,
      }),
      { wrapper: wrapper(queryClient) },
    );
    let startPromise: Promise<string>;
    act(() => {
      startPromise = result.current.start({
        channel_id: "channel-1",
        agent_id: "agent-1",
      });
    });
    let firstHangUp: Promise<void>;
    act(() => {
      firstHangUp = result.current.hangUp();
    });
    resolveCreate?.(createdCall);

    await act(async () => {
      await expect(firstHangUp).rejects.toThrow("stop endpoint unavailable");
    });
    await expect(startPromise!).rejects.toThrow("stop endpoint unavailable");
    expect(stopVoiceCall).toHaveBeenCalledTimes(1);
    expect(result.current.error).toMatchObject({
      source: "stop",
      code: "stop_failed",
    });

    await act(async () => {
      await result.current.hangUp();
    });
    expect(stopVoiceCall).toHaveBeenCalledTimes(2);
    expect(result.current.phase).toBe("ended");
  });




  it("prefers duplex and falls back to RTC when duplex is not configured", async () => {
    const media = createFakeMediaSession();
    const duplex = createFakeDuplexSession();
    const { result } = renderHook(
      () => useVoiceCallController("workspace-1", {
        mediaSessionFactory: media.factory,
        duplexSessionFactory: duplex.factory,
      }),
      { wrapper: wrapper(queryClient) },
    );

    await act(async () => {
      await result.current.start({
        channel_id: "channel-1",
        agent_id: "agent-1",
      });
    });

    expect(preflightMicrophoneAccess).toHaveBeenCalledOnce();
    expect(startVoiceCallDuplex).toHaveBeenCalledWith("workspace-1", "call-1");
    expect(duplex.factory).not.toHaveBeenCalled();
    expect(media.factory).toHaveBeenCalledTimes(1);
    expect(connectVoiceCall).toHaveBeenCalledWith("workspace-1", "call-1");
    expect(result.current.mode).toBe("rtc");
  });

  it("keeps duplex joining until TTS playback starts", async () => {
    startVoiceCallDuplex.mockResolvedValue({
      call: {
        ...createdCall.call,
        status: "active",
        connected_at: "2026-08-01T00:12:00Z",
      },
      mode: "duplex",
      ws_path: "/api/workspaces/workspace-1/voice-calls/call-1/duplex/ws",
      audio: {
        input_format: "pcm_s16le",
        input_sample_rate: 16000,
        output_format: "pcm_s16le",
        output_sample_rate: 24000,
      },
      events: { client: [], server: [] },
    });
    const duplex = createFakeDuplexSession(async (events) => {
      events.onReady?.("duplex-session-1");
    });
    const ringback = createFakeRingback();
    const { result } = renderHook(
      () => useVoiceCallController("workspace-1", {
        duplexSessionFactory: duplex.factory,
        mediaSessionFactory: createFakeMediaSession().factory,
        ringbackFactory: ringback.factory,
      }),
      { wrapper: wrapper(queryClient) },
    );

    await act(async () => {
      await result.current.start({
        channel_id: "channel-1",
        agent_id: "agent-1",
      });
    });

    expect(result.current.mode).toBe("duplex");
    expect(result.current.phase).toBe("joining");
    expect(ringback.ringback.stop).not.toHaveBeenCalled();

    await act(async () => {
      duplex.events().onPlaybackStarted?.();
    });

    expect(result.current.phase).toBe("connected");
    expect(ringback.ringback.stop).toHaveBeenCalled();
  });

  it("connects duplex media when activation succeeds", async () => {
    startVoiceCallDuplex.mockResolvedValue({
      call: {
        ...createdCall.call,
        status: "active",
        connected_at: "2026-08-01T00:12:00Z",
      },
      mode: "duplex",
      ws_path: "/api/workspaces/workspace-1/voice-calls/call-1/duplex/ws",
      audio: {
        input_format: "pcm_s16le",
        input_sample_rate: 16000,
        output_format: "pcm_s16le",
        output_sample_rate: 24000,
      },
      events: { client: [], server: [] },
    });
    const duplex = createFakeDuplexSession();
    const media = createFakeMediaSession();
    const ringback = createFakeRingback();
    const { result } = renderHook(
      () => useVoiceCallController("workspace-1", {
        mediaSessionFactory: media.factory,
        duplexSessionFactory: duplex.factory,
        ringbackFactory: ringback.factory,
      }),
      { wrapper: wrapper(queryClient) },
    );

    await act(async () => {
      await result.current.start({
        channel_id: "channel-1",
        agent_id: "agent-1",
      });
    });

    expect(duplex.session.connect).toHaveBeenCalledWith(
      "wss://api.example.test/api/workspaces/workspace-1/voice-calls/call-1/duplex/ws",
      undefined,
    );
    expect(connectVoiceCall).not.toHaveBeenCalled();
    expect(answerVoiceCall).not.toHaveBeenCalled();
    expect(result.current.mode).toBe("duplex");
    expect(result.current.phase).toBe("connected");
    expect(ringback.ringback.stop).toHaveBeenCalled();
  });

  it("surfaces duplex tool progress from server events", async () => {
    startVoiceCallDuplex.mockResolvedValue({
      call: createdCall.call,
      mode: "duplex",
      ws_path: "/api/workspaces/workspace-1/voice-calls/call-1/duplex/ws",
      audio: {
        input_format: "pcm_s16le",
        input_sample_rate: 16000,
        output_format: "pcm_s16le",
        output_sample_rate: 24000,
      },
      events: { client: [], server: [] },
    });
    const duplex = createFakeDuplexSession(async (events) => {
      events.onTool?.({
        name: "delegate_work_to_multica_agent",
        status: "started",
      });
      events.onReady?.("duplex-session-1");
    });
    const { result } = renderHook(
      () => useVoiceCallController("workspace-1", {
        duplexSessionFactory: duplex.factory,
        mediaSessionFactory: createFakeMediaSession().factory,
      }),
      { wrapper: wrapper(queryClient) },
    );

    await act(async () => {
      await result.current.start({
        channel_id: "channel-1",
        agent_id: "agent-1",
      });
    });

    expect(result.current.toolStatus).toMatchObject({
      name: "delegate_work_to_multica_agent",
      status: "started",
    });
  });
});
