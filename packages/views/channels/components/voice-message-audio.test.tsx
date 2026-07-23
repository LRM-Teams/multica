import { cleanup, render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import type { Attachment, ChannelMessage } from "@multica/core/types";
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
          voice_transcribing: "Transcribing",
          voice_transcription_unavailable: "Transcript unavailable",
          voice_synthesizing: "Generating voice",
          voice_synthesis_unavailable: "Voice unavailable",
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

function humanRecordingMessage(
  transcriptionStatus: "pending" | "completed" | "failed",
): ChannelMessage {
  const attachment: Attachment = {
    id: "recording-status",
    workspace_id: "workspace-1",
    issue_id: null,
    comment_id: null,
    chat_session_id: null,
    chat_message_id: null,
    uploader_type: "member",
    uploader_id: "user-1",
    filename: "voice-status.wav",
    url: "/uploads/voice-status.wav",
    download_url: "/api/attachments/recording-status/download",
    markdown_url: "/api/attachments/recording-status/download",
    content_type: "audio/wav",
    size_bytes: 48,
    created_at: "2026-07-23T10:00:00.000Z",
  };
  return agentVoiceMessage({
    type: "user",
    author_id: "user-1",
    author_name: "Alice",
    content: transcriptionStatus === "completed" ? "Recorded question" : "",
    parts: [
      ...(transcriptionStatus === "completed"
        ? [{ type: "text" as const, text: "Recorded question" }]
        : []),
      {
        type: "voice",
        duration_ms: 1800,
        attachment_id: attachment.id,
        transcription_status: transcriptionStatus,
      },
    ],
    attachments: [attachment],
  });
}

function agentSynthesisMessage(
  synthesisStatus: "pending" | "completed" | "failed",
): ChannelMessage {
  const attachment: Attachment = {
    id: "agent-voice-status",
    workspace_id: "workspace-1",
    issue_id: null,
    comment_id: null,
    chat_session_id: null,
    chat_message_id: null,
    uploader_type: "agent",
    uploader_id: "agent-1",
    filename: "voice-message-1.wav",
    url: "/uploads/voice-message-1.wav",
    download_url: "/api/attachments/agent-voice-status/download",
    markdown_url: "/api/attachments/agent-voice-status/download",
    content_type: "audio/wav",
    size_bytes: 4844,
    created_at: "2026-07-23T10:00:00.000Z",
  };
  const completed = synthesisStatus === "completed";
  return agentVoiceMessage({
    parts: [
      { type: "text", text: "Spoken answer" },
      {
        type: "voice",
        synthesis_status: synthesisStatus,
        ...(completed
          ? {
              attachment_id: attachment.id,
              filename: attachment.filename,
              content_type: attachment.content_type,
              size_bytes: attachment.size_bytes,
              duration_ms: 100,
            }
          : {}),
      },
    ],
    attachments: completed ? [attachment] : [],
  });
}

describe("VoiceMessageAudio", () => {
  let mediaPlay: ReturnType<typeof vi.spyOn>;

  beforeEach(() => {
    playbackMocks.claimVoiceAutoplay.mockReset().mockReturnValue(true);
    playbackMocks.stop.mockReset();
    playbackMocks.prepareVoiceAudio.mockReset().mockResolvedValue({
      audio: new ArrayBuffer(4),
      durationMs: 3200,
    });
    mediaPlay = vi.spyOn(HTMLMediaElement.prototype, "play").mockResolvedValue(undefined);
    vi.spyOn(HTMLMediaElement.prototype, "pause").mockImplementation(() => {});
    playbackMocks.startPreparedVoicePlayback.mockReset().mockResolvedValue({
      durationMs: 3200,
      finished: new Promise<void>(() => {}),
      stop: playbackMocks.stop,
    });
  });

  afterEach(() => {
    cleanup();
    vi.restoreAllMocks();
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

  it("plays a human recording attachment without calling TTS", async () => {
    playbackMocks.claimVoiceAutoplay.mockReturnValue(false);
    const attachment = {
      id: "recording-1",
      workspace_id: "workspace-1",
      issue_id: null,
      comment_id: null,
      chat_session_id: null,
      chat_message_id: null,
      uploader_type: "member" as const,
      uploader_id: "user-1",
      filename: "voice-recording.wav",
      url: "/uploads/voice-recording.wav",
      download_url: "/api/attachments/recording-1/download",
      markdown_url: "/api/attachments/recording-1/download",
      content_type: "audio/wav",
      size_bytes: 48,
      created_at: "2026-07-22T10:00:01.000Z",
    };
    render(<VoiceMessageAudio message={agentVoiceMessage({
      type: "user",
      author_id: "user-1",
      author_name: "Alice",
      content: "Recorded question",
      parts: [
        { type: "text", text: "Recorded question" },
        {
          type: "voice",
          duration_ms: 1800,
          attachment_id: attachment.id,
          filename: attachment.filename,
          content_type: attachment.content_type,
          size_bytes: attachment.size_bytes,
        },
      ],
      attachments: [attachment],
    })} />);

    expect(screen.queryByText("Recorded question")).not.toBeInTheDocument();
    await userEvent.click(screen.getByRole("button", { name: "Play voice reply" }));
    expect(mediaPlay).toHaveBeenCalledOnce();
    expect(playbackMocks.prepareVoiceAudio).not.toHaveBeenCalled();

  });

  it("plays a newly sent recording before its server transcript exists", async () => {
    playbackMocks.claimVoiceAutoplay.mockReturnValue(false);
    const attachment = {
      id: "recording-pending",
      workspace_id: "workspace-1",
      issue_id: null,
      comment_id: null,
      chat_session_id: null,
      chat_message_id: null,
      uploader_type: "member" as const,
      uploader_id: "user-1",
      filename: "voice-pending.wav",
      url: "/uploads/voice-pending.wav",
      download_url: "/api/attachments/recording-pending/download",
      markdown_url: "/api/attachments/recording-pending/download",
      content_type: "audio/wav",
      size_bytes: 48,
      created_at: "2026-07-22T10:00:01.000Z",
    };
    render(<VoiceMessageAudio message={agentVoiceMessage({
      type: "user",
      author_id: "user-1",
      author_name: "Alice",
      content: "",
      parts: [{
        type: "voice",
        duration_ms: 1800,
        attachment_id: attachment.id,
        filename: attachment.filename,
        content_type: attachment.content_type,
        size_bytes: attachment.size_bytes,
      }],
      attachments: [attachment],
    })} />);

    expect(screen.queryByRole("button", { name: "Show transcript" })).not.toBeInTheDocument();
    await userEvent.click(screen.getByRole("button", { name: "Play voice reply" }));

    expect(mediaPlay).toHaveBeenCalledOnce();
    expect(playbackMocks.prepareVoiceAudio).not.toHaveBeenCalled();
  });

  it("shows pending transcription beside the voice bubble", () => {
    playbackMocks.claimVoiceAutoplay.mockReturnValue(false);
    render(<VoiceMessageAudio message={humanRecordingMessage("pending")} />);

    expect(screen.getByRole("status")).toHaveTextContent("Transcribing");
    expect(screen.getByRole("button", { name: "Play voice reply" })).toBeEnabled();
    expect(screen.queryByRole("button", { name: "Show transcript" })).not.toBeInTheDocument();
  });

  it("scopes a transcription failure to the transcript action", () => {
    playbackMocks.claimVoiceAutoplay.mockReturnValue(false);
    render(<VoiceMessageAudio message={humanRecordingMessage("failed")} />);

    expect(screen.getByRole("status")).toHaveTextContent("Transcript unavailable");
    expect(screen.getByRole("button", { name: "Play voice reply" })).toBeEnabled();
    expect(screen.queryByRole("button", { name: "Show transcript" })).not.toBeInTheDocument();
  });

  it("shows the transcript action only after server transcription completes", () => {
    playbackMocks.claimVoiceAutoplay.mockReturnValue(false);
    render(<VoiceMessageAudio message={humanRecordingMessage("completed")} />);

    expect(screen.queryByRole("status")).not.toBeInTheDocument();
    expect(screen.getByRole("button", { name: "Show transcript" })).toBeEnabled();
  });

  it("waits for server-owned Agent synthesis without calling browser TTS", () => {
    render(<VoiceMessageAudio message={agentSynthesisMessage("pending")} />);

    expect(screen.getByRole("button", { name: "Generating voice" })).toBeDisabled();
    expect(screen.getByRole("status")).toHaveTextContent("Generating voice");
    expect(screen.getByRole("button", { name: "Show transcript" })).toBeEnabled();
    expect(playbackMocks.prepareVoiceAudio).not.toHaveBeenCalled();
    expect(playbackMocks.claimVoiceAutoplay).not.toHaveBeenCalled();
  });

  it("shows terminal server synthesis failure without retrying in the browser", () => {
    render(<VoiceMessageAudio message={agentSynthesisMessage("failed")} />);

    expect(screen.getByRole("button", { name: "Voice unavailable" })).toBeDisabled();
    expect(screen.getByRole("status")).toHaveTextContent("Voice unavailable");
    expect(screen.getByRole("button", { name: "Show transcript" })).toBeEnabled();
    expect(playbackMocks.prepareVoiceAudio).not.toHaveBeenCalled();
    expect(playbackMocks.claimVoiceAutoplay).not.toHaveBeenCalled();
  });

  it("autoplays the persisted Agent WAV only after synthesis completes", async () => {
    const rendered = render(
      <VoiceMessageAudio message={agentSynthesisMessage("pending")} />,
    );

    expect(playbackMocks.claimVoiceAutoplay).not.toHaveBeenCalled();
    rendered.rerender(
      <VoiceMessageAudio message={agentSynthesisMessage("completed")} />,
    );

    await waitFor(() => {
      expect(playbackMocks.claimVoiceAutoplay).toHaveBeenCalledOnce();
      expect(mediaPlay).toHaveBeenCalledOnce();
    });
    expect(playbackMocks.prepareVoiceAudio).not.toHaveBeenCalled();
  });

  it("does not synthesize a human recording when its media URL is unavailable", async () => {
    playbackMocks.claimVoiceAutoplay.mockReturnValue(false);
    render(<VoiceMessageAudio message={agentVoiceMessage({
      type: "user",
      author_id: "user-1",
      content: "Recorded question",
      parts: [
        { type: "text", text: "Recorded question" },
        { type: "voice", duration_ms: 1800, attachment_id: "recording-1" },
      ],
      attachments: [{
        id: "recording-1",
        workspace_id: "workspace-1",
        issue_id: null,
        comment_id: null,
        chat_session_id: null,
        chat_message_id: null,
        uploader_type: "member",
        uploader_id: "user-1",
        filename: "voice-recording.wav",
        url: "",
        download_url: "",
        markdown_url: "",
        content_type: "audio/wav",
        size_bytes: 48,
        created_at: "2026-07-22T10:00:01.000Z",
      }],
    })} />);

    await userEvent.click(screen.getByRole("button", { name: "Play voice reply" }));

    expect(await screen.findByRole("button", {
      name: "Voice playback failed · Retry",
    })).toBeInTheDocument();
    expect(mediaPlay).not.toHaveBeenCalled();
    expect(playbackMocks.prepareVoiceAudio).not.toHaveBeenCalled();
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
