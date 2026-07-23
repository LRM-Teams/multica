import { render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { beforeEach, describe, expect, it, vi } from "vitest";
import type { ChannelMessage } from "@multica/core/types";
import { VoiceMessageAudio } from "./voice-message-audio";
import { voiceBubbleWidthPx } from "../lib/voice-message-presentation";

const playbackMocks = vi.hoisted(() => ({
  claimVoiceAutoplay: vi.fn(),
  prepareVoiceAudio: vi.fn(),
  startPreparedVoicePlayback: vi.fn(),
  stop: vi.fn(),
}));

vi.mock("../lib/voice-playback", () => ({
  claimVoiceAutoplay: playbackMocks.claimVoiceAutoplay,
  prepareVoiceAudio: playbackMocks.prepareVoiceAudio,
  startPreparedVoicePlayback: playbackMocks.startPreparedVoicePlayback,
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
          voice_show_transcript: "Show transcript",
          voice_hide_transcript: "Hide transcript",
          voice_transcript_label: "Voice transcript",
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
    playbackMocks.prepareVoiceAudio.mockReset().mockResolvedValue({
      audio: new ArrayBuffer(4),
      durationMs: 3200,
    });
    playbackMocks.startPreparedVoicePlayback.mockReset().mockResolvedValue({
      durationMs: 3200,
      finished: new Promise<void>(() => {}),
      stop: playbackMocks.stop,
    });
  });

  it("autoplays an eligible Agent voice reply and stops it on unmount", async () => {
    const rendered = render(<VoiceMessageAudio message={agentVoiceMessage()} />);

    await waitFor(() => {
      expect(playbackMocks.prepareVoiceAudio).toHaveBeenCalledWith("Spoken answer");
      expect(playbackMocks.startPreparedVoicePlayback).toHaveBeenCalledOnce();
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
    await waitFor(() => expect(playbackMocks.startPreparedVoicePlayback).toHaveBeenCalledOnce());

    rendered.rerender(<VoiceMessageAudio message={agentVoiceMessage()} />);

    expect(playbackMocks.stop).not.toHaveBeenCalled();
    expect(playbackMocks.startPreparedVoicePlayback).toHaveBeenCalledOnce();
  });

  it("renders an Agent reply as a playable voice bubble with decoded duration", async () => {
    playbackMocks.claimVoiceAutoplay.mockReturnValue(false);
    render(<VoiceMessageAudio message={agentVoiceMessage()} />);

    const bubble = screen.getByRole("button", { name: "Play voice reply" });
    expect(bubble).toHaveAttribute("data-voice-bubble", "true");
    expect(bubble).not.toHaveTextContent("Play voice reply");

    await userEvent.click(bubble);

    await waitFor(() => expect(bubble).toHaveTextContent('3″'));
    expect(playbackMocks.startPreparedVoicePlayback).toHaveBeenCalledOnce();
    expect(bubble).toHaveStyle({ width: "136px" });
  });

  it("renders the production Agent audio attachment as a TTS voice bubble", async () => {
    playbackMocks.claimVoiceAutoplay.mockReturnValue(false);
    render(<VoiceMessageAudio message={agentVoiceMessage({
      content: "你好～",
      parts: [
        { type: "text", text: "你好～" },
        { type: "attachment", attachment_id: "audio-1" },
      ],
      attachments: [{
        id: "audio-1",
        workspace_id: "workspace-1",
        issue_id: null,
        comment_id: null,
        chat_session_id: null,
        chat_message_id: null,
        uploader_type: "agent",
        uploader_id: "agent-1",
        filename: "nihao.mp3",
        url: "/uploads/nihao.mp3",
        download_url: "/api/attachments/audio-1/download",
        markdown_url: "/api/attachments/audio-1/download",
        content_type: "audio/mpeg",
        size_bytes: 13_940,
        created_at: "2026-07-22T10:00:01.000Z",
      }],
    })} />);

    const bubble = screen.getByRole("button", { name: "Play voice reply" });
    await waitFor(() => expect(bubble).toHaveTextContent('3″'));
    expect(screen.queryByText("nihao.mp3")).not.toBeInTheDocument();
    expect(playbackMocks.prepareVoiceAudio).toHaveBeenCalledWith("你好～");
  });

  it("grows the bubble by real duration within fixed bounds", () => {
    expect(voiceBubbleWidthPx(3)).toBeLessThan(voiceBubbleWidthPx(5));
    expect(voiceBubbleWidthPx(0)).toBe(112);
    expect(voiceBubbleWidthPx(60)).toBe(240);
  });

  it("shows a retry state when background TTS preparation fails", async () => {
    playbackMocks.claimVoiceAutoplay.mockReturnValue(false);
    playbackMocks.prepareVoiceAudio.mockRejectedValue(new Error("provider unavailable"));

    render(<VoiceMessageAudio message={agentVoiceMessage()} />);

    expect(await screen.findByRole("button", {
      name: "Voice playback failed · Retry",
    })).toBeInTheDocument();
  });
});
