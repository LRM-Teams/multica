/**
 * @vitest-environment jsdom
 */
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { act, renderHook } from "@testing-library/react";
import type { ReactNode } from "react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { setApiInstance } from "../api";
import type { ApiClient } from "../api/client";
import type {
  CreateVoiceCallResponse,
  GetVoiceCallResponse,
} from "../types";
import { useCreateVoiceCall, useStopVoiceCall } from "./mutations";
import { voiceCallKeys } from "./queries";

const activeCall: GetVoiceCallResponse = {
  call: {
    id: "call-1",
    channel_id: "channel-1",
    agent_id: "agent-1",
    status: "active",
    started_at: "2026-07-23T10:00:00Z",
    connected_at: "2026-07-23T10:00:01Z",
    ended_at: null,
    end_reason: "",
    error_code: "",
    input_audio_ms: 0,
    output_audio_ms: 0,
    updated_at: "2026-07-23T10:00:01Z",
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

describe("voice call mutations", () => {
  let queryClient: QueryClient;

  beforeEach(() => {
    queryClient = new QueryClient({
      defaultOptions: {
        queries: { retry: false },
        mutations: { retry: false },
      },
    });
  });

  afterEach(() => {
    queryClient.clear();
    vi.restoreAllMocks();
  });

  it("caches only durable call state after create, never RTC credentials", async () => {
    const created: CreateVoiceCallResponse = {
      ...activeCall,
      media: {
        app_id: "rtc-app",
        room_id: "room-1",
        user_id: "voice-member-1",
        token: "short-lived-secret",
        expires_at: "2026-07-23T10:05:00Z",
      },
    };
    setApiInstance({
      createVoiceCall: vi.fn().mockResolvedValue(created),
    } as unknown as ApiClient);
    const { result } = renderHook(
      () => useCreateVoiceCall("workspace-1"),
      { wrapper: wrapper(queryClient) },
    );

    await act(async () => {
      await result.current.mutateAsync({
        channel_id: "channel-1",
        agent_id: "agent-1",
      });
    });

    const cached = queryClient.getQueryData(
      voiceCallKeys.detail("workspace-1", "call-1"),
    );
    expect(cached).toEqual(activeCall);
    expect(JSON.stringify(cached)).not.toContain("short-lived-secret");
  });


  it("replaces optimistic ending state with the server terminal state", async () => {
    const ended: GetVoiceCallResponse = {
      call: {
        ...activeCall.call,
        status: "ended",
        ended_at: "2026-07-23T10:02:00Z",
        end_reason: "user_hangup",
      },
    };
    setApiInstance({
      stopVoiceCall: vi.fn().mockResolvedValue(ended),
    } as unknown as ApiClient);
    const key = voiceCallKeys.detail("workspace-1", "call-1");
    queryClient.setQueryData(key, activeCall);
    const { result } = renderHook(
      () => useStopVoiceCall("workspace-1"),
      { wrapper: wrapper(queryClient) },
    );

    await act(async () => {
      await result.current.mutateAsync("call-1");
    });

    expect(queryClient.getQueryData(key)).toEqual(ended);
  });

  it("targets the call ID supplied when the stop begins", async () => {
    const stopVoiceCall = vi.fn().mockResolvedValue(activeCall);
    setApiInstance({ stopVoiceCall } as unknown as ApiClient);
    const { result } = renderHook(
      () => useStopVoiceCall("workspace-1"),
      { wrapper: wrapper(queryClient) },
    );

    await act(async () => {
      await result.current.mutateAsync("call-created-after-render");
    });

    expect(stopVoiceCall).toHaveBeenCalledWith(
      "workspace-1",
      "call-created-after-render",
    );
  });
});
