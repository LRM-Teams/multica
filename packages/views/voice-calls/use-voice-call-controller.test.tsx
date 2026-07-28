/**
 * @vitest-environment jsdom
 */
import { setApiInstance } from "@multica/core/api";
import type { ApiClient } from "@multica/core/api/client";
import { voiceCallKeys } from "@multica/core/voice-calls";
import type {
  CreateVoiceCallResponse,
  GetVoiceCallResponse,
} from "@multica/core/types";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { act, renderHook, waitFor } from "@testing-library/react";
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

const createdCall: CreateVoiceCallResponse = {
  call: {
    id: "call-1",
    channel_id: "channel-1",
    agent_id: "agent-1",
    status: "connecting",
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
  let stopVoiceCall: ReturnType<typeof vi.fn>;

  beforeEach(() => {
    queryClient = new QueryClient({
      defaultOptions: {
        queries: { retry: false },
        mutations: { retry: false },
      },
    });
    createVoiceCall = vi.fn().mockResolvedValue(createdCall);
    stopVoiceCall = vi.fn().mockResolvedValue(endedCall);
    setApiInstance({
      createVoiceCall,
      stopVoiceCall,
      getVoiceCall: vi.fn().mockResolvedValue({ call: createdCall.call }),
    } as unknown as ApiClient);
  });

  afterEach(() => {
    queryClient.clear();
    vi.restoreAllMocks();
  });

  it("plays ringback while connecting and stops it when media connects", async () => {
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

    expect(result.current.phase).toBe("connected");
    expect(ringback.ringback.stop).toHaveBeenCalledTimes(1);
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
    expect(result.current.phase).toBe("connected");
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

  it("stops the server call after an active provider media failure", async () => {
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
    });

    act(() => {
      media.events().onError?.(
        new VoiceCallMediaError("provider_error", "RTC connection failed"),
      );
    });

    await waitFor(() => {
      expect(stopVoiceCall).toHaveBeenCalledTimes(1);
    });
    expect(result.current.phase).toBe("failed");
    expect(result.current.error?.code).toBe("provider_error");
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

  it("controls mute and recovers browser-blocked remote playback", async () => {
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
      await result.current.setMuted(true);
    });
    expect(result.current.phase).toBe("muted");

    act(() => {
      media.events().onAutoplayBlocked?.("voice-agent-1");
    });
    await act(async () => {
      await result.current.resumeRemoteAudio();
    });

    expect(media.session.resumeRemoteAudio)
      .toHaveBeenCalledWith("voice-agent-1");
    expect(result.current.autoplayBlockedUserId).toBeNull();
  });

  it("disconnects media when realtime state reports a terminal server failure", async () => {
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
    });

    act(() => {
      queryClient.setQueryData<GetVoiceCallResponse>(
        voiceCallKeys.detail("workspace-1", "call-1"),
        {
          call: {
            ...createdCall.call,
            status: "failed",
            ended_at: "2026-07-23T10:03:00Z",
            error_code: "provider_failed",
          },
        },
      );
    });

    await waitFor(() => {
      expect(media.session.disconnect).toHaveBeenCalledTimes(1);
    });
    expect(result.current.phase).toBe("failed");
    expect(result.current.error).toMatchObject({
      source: "server",
      code: "provider_failed",
    });
  });

  it("disconnects media and requests server stop when unmounted", async () => {
    const media = createFakeMediaSession();
    const { result, unmount } = renderHook(
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
    });

    unmount();

    await waitFor(() => {
      expect(stopVoiceCall).toHaveBeenCalledWith("workspace-1", "call-1");
    });
    expect(media.session.disconnect).toHaveBeenCalledTimes(1);
  });
});
