import { render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { beforeEach, describe, expect, it, vi } from "vitest";
import type { ChannelMessage } from "@multica/core/types";
import { VoiceMessageAudio } from "./voice-message-audio";

const playbackMocks = vi.hoisted(() => ({
  claimVoiceAutoplay: vi.fn(),
  startVoicePlayback: vi.fn(),
  stop: vi.fn(),
}));

vi.mock("../lib/voice-playback", () => ({
  claimVoiceAutoplay: playbackMocks.claimVoiceAutoplay,
  startVoicePlayback: playbackMocks.startVoicePlayback,
  voicePlaybackScope: (channelId: string, threadRootMessageId?: string | null) =>
    `${channelId}:${threadRootMessageId ?? "main"}`,
}));

vi.mock("../../i18n/use-t", () => ({
  useT: () => ({
    t: (selector: (resources: Record<string, unknown>) => string) =>
      selector({
        message: {
          voice_input: "Voice input",
          voice_input_duration: "Voice input · {{seconds}}s",
          voice_play: "Play voice reply",
          voice_loading: "Preparing voice reply…",
          voice_stop: "Stop voice reply",
          voice_retry: "Voice playback failed · Retry",
        },
      }),
  }),
}));

function agentVoiceMessage(overrides: Partial<ChannelMessage> = {}): ChannelMessage {
  return {
    id: "message-1",
    channel_id: "channel-1",
    workspace_id: "workspace-1",
    type: "agent",
    author_id: "agent-1",
    author_name: "Beckham",
    content: "Spoken answer",
    source: "multica",
    external_message_id: null,
    client_message_id: null,
    created_at: "2026-07-22T10:00:01.000Z",
    seq: 1,
    parts: [{ type: "text", text: "Spoken answer" }, { type: "voice" }],
    ...overrides,
  };
}

describe("VoiceMessageAudio", () => {
  beforeEach(() => {
    playbackMocks.claimVoiceAutoplay.mockReset().mockReturnValue(true);
    playbackMocks.stop.mockReset();
    playbackMocks.startVoicePlayback.mockReset().mockResolvedValue({
      durationMs: 3200,
      finished: new Promise<void>(() => {}),
      stop: playbackMocks.stop,
    });
  });

  it("autoplays an eligible Agent voice reply and stops it on unmount", async () => {
    const rendered = render(<VoiceMessageAudio message={agentVoiceMessage()} />);

    await waitFor(() => {
      expect(playbackMocks.startVoicePlayback).toHaveBeenCalledWith("Spoken answer");
    });
    expect(playbackMocks.claimVoiceAutoplay).toHaveBeenCalledWith(
      "message-1",
      "channel-1:main",
      "2026-07-22T10:00:01.000Z",
    );

    rendered.unmount();
    expect(playbackMocks.stop).toHaveBeenCalledOnce();
  });

  it("does not interrupt playback when equivalent message parts are refreshed", async () => {
    const rendered = render(<VoiceMessageAudio message={agentVoiceMessage()} />);
    await waitFor(() => expect(playbackMocks.startVoicePlayback).toHaveBeenCalledOnce());

    rendered.rerender(<VoiceMessageAudio message={agentVoiceMessage()} />);

    expect(playbackMocks.stop).not.toHaveBeenCalled();
    expect(playbackMocks.startVoicePlayback).toHaveBeenCalledOnce();
  });

  it("renders an Agent reply as a playable voice bubble with decoded duration", async () => {
    playbackMocks.claimVoiceAutoplay.mockReturnValue(false);
    render(<VoiceMessageAudio message={agentVoiceMessage()} />);

    const bubble = screen.getByRole("button", { name: "Play voice reply" });
    expect(bubble).toHaveAttribute("data-voice-bubble", "true");
    expect(bubble).not.toHaveTextContent("Play voice reply");

    await userEvent.click(bubble);

    await waitFor(() => expect(bubble).toHaveTextContent('3″'));
    expect(playbackMocks.startVoicePlayback).toHaveBeenCalledWith("Spoken answer");
  });
});
