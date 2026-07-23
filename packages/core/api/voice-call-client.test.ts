import { afterEach, describe, expect, it, vi } from "vitest";
import { noopLogger } from "../logger";
import { ApiClient, ApiError } from "./client";
import { setSchemaLogger } from "./schema";
import { EMPTY_GET_VOICE_CALL_RESPONSE } from "./schemas";

const call = {
  id: "call-1",
  channel_id: "channel-1",
  agent_id: "agent-1",
  status: "connecting",
  started_at: "2026-07-23T10:00:00Z",
  input_audio_ms: 0,
  output_audio_ms: 0,
  updated_at: "2026-07-23T10:00:01Z",
};

afterEach(() => {
  vi.unstubAllGlobals();
  setSchemaLogger(noopLogger);
});

describe("ApiClient voice calls", () => {
  it("creates a call with the exact workspace-scoped request contract", async () => {
    const fetchMock = vi.fn().mockResolvedValue(
      Response.json({
        call,
        media: {
          app_id: "rtc-app",
          room_id: "voice-call-1",
          user_id: "voice-member-1",
          token: "short-lived-token",
          expires_at: "2026-07-23T10:05:00Z",
        },
      }, { status: 201 }),
    );
    vi.stubGlobal("fetch", fetchMock);
    const client = new ApiClient("https://api.example.test");

    await expect(
      client.createVoiceCall("workspace/1", {
        channel_id: "channel-1",
        agent_id: "agent-1",
      }),
    ).resolves.toMatchObject({
      call: {
        id: "call-1",
        status: "connecting",
        connected_at: null,
        ended_at: null,
      },
      media: {
        room_id: "voice-call-1",
        token: "short-lived-token",
      },
    });
    expect(fetchMock).toHaveBeenCalledWith(
      "https://api.example.test/api/workspaces/workspace%2F1/voice-calls",
      expect.objectContaining({
        method: "POST",
        body: JSON.stringify({
          channel_id: "channel-1",
          agent_id: "agent-1",
        }),
      }),
    );
  });

  it("fails closed when create succeeds without usable RTC credentials", async () => {
    const warn = vi.fn();
    setSchemaLogger({
      debug: vi.fn(),
      info: vi.fn(),
      warn,
      error: vi.fn(),
    });
    vi.stubGlobal("fetch", vi.fn().mockResolvedValue(
      Response.json({
        call,
        media: {
          app_id: "rtc-app",
          room_id: "voice-call-1",
          user_id: "voice-member-1",
          unexpected_token_field: "must-not-enter-schema-logs",
          expires_at: "2026-07-23T10:05:00Z",
        },
      }, { status: 201 }),
    ));
    const client = new ApiClient("https://api.example.test");

    await expect(
      client.createVoiceCall("workspace-1", {
        channel_id: "channel-1",
        agent_id: "agent-1",
      }),
    ).rejects.toEqual(expect.objectContaining({
      name: ApiError.name,
      status: 502,
    }));
    expect(warn).toHaveBeenCalledOnce();
    expect(JSON.stringify(warn.mock.calls)).not.toContain("must-not-enter-schema-logs");
    expect(JSON.stringify(warn.mock.calls)).toContain("[redacted voice call response]");
  });

  it("preserves unknown server status for the UI default branch", async () => {
    vi.stubGlobal("fetch", vi.fn().mockResolvedValue(
      Response.json({ call: { ...call, status: "provider_paused" } }),
    ));
    const client = new ApiClient("https://api.example.test");

    await expect(client.getVoiceCall("workspace-1", "call/1")).resolves.toMatchObject({
      call: { status: "provider_paused" },
    });
    expect(fetch).toHaveBeenCalledWith(
      "https://api.example.test/api/workspaces/workspace-1/voice-calls/call%2F1",
      expect.any(Object),
    );
  });

  it("returns the safe unknown call when a get response is malformed", async () => {
    vi.stubGlobal("fetch", vi.fn().mockResolvedValue(Response.json({ call: null })));
    const client = new ApiClient("https://api.example.test");

    await expect(client.getVoiceCall("workspace-1", "call-1"))
      .resolves.toEqual(EMPTY_GET_VOICE_CALL_RESPONSE);
  });

  it("stops a call through the idempotent stop endpoint", async () => {
    const fetchMock = vi.fn().mockResolvedValue(
      Response.json({
        call: {
          ...call,
          status: "ended",
          ended_at: "2026-07-23T10:02:00Z",
          end_reason: "user_hangup",
        },
      }),
    );
    vi.stubGlobal("fetch", fetchMock);
    const client = new ApiClient("https://api.example.test");

    await expect(client.stopVoiceCall("workspace-1", "call-1")).resolves.toMatchObject({
      call: {
        status: "ended",
        end_reason: "user_hangup",
      },
    });
    expect(fetchMock).toHaveBeenCalledWith(
      "https://api.example.test/api/workspaces/workspace-1/voice-calls/call-1/stop",
      expect.objectContaining({ method: "POST" }),
    );
  });
});
