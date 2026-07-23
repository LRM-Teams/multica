import { beforeEach, describe, expect, it, vi } from "vitest";
import { deliverVoiceRecording } from "./voice-recording-delivery";

const apiMocks = vi.hoisted(() => ({
  deleteAttachment: vi.fn(),
  transcribeVoice: vi.fn(),
  uploadFile: vi.fn(),
}));

vi.mock("@multica/core/api", () => ({ api: apiMocks }));

describe("deliverVoiceRecording", () => {
  beforeEach(() => {
    apiMocks.deleteAttachment.mockReset().mockResolvedValue(undefined);
    apiMocks.transcribeVoice.mockReset().mockResolvedValue("spoken question");
    apiMocks.uploadFile.mockReset().mockResolvedValue({
      id: "recording-1",
      filename: "voice-recording.wav",
      content_type: "audio/wav",
      size_bytes: 48,
    });
  });

  it("uploads the WAV and transcribes the same PCM concurrently", async () => {
    let releaseTranscript: ((value: string) => void) | undefined;
    apiMocks.transcribeVoice.mockReturnValue(new Promise((resolve) => {
      releaseTranscript = resolve;
    }));
    const pending = deliverVoiceRecording(new ArrayBuffer(4), "channel-1", 123);

    expect(apiMocks.uploadFile).toHaveBeenCalledWith(
      expect.objectContaining({ name: "voice-123.wav", type: "audio/wav" }),
      { channelId: "channel-1" },
    );
    releaseTranscript?.("spoken question");

    await expect(pending).resolves.toEqual({
      transcript: "spoken question",
      attachment: expect.objectContaining({ id: "recording-1" }),
    });
  });

  it("deletes an uploaded orphan when ASR fails", async () => {
    apiMocks.transcribeVoice.mockRejectedValue(new Error("ASR failed"));

    await expect(deliverVoiceRecording(new ArrayBuffer(4), "channel-1", 123)).rejects.toThrow(
      "ASR failed",
    );
    expect(apiMocks.deleteAttachment).toHaveBeenCalledWith("recording-1");
  });

  it("rejects an upload response without a bindable attachment id", async () => {
    apiMocks.uploadFile.mockResolvedValue({
      id: "",
      filename: "voice-recording.wav",
      content_type: "audio/wav",
      size_bytes: 48,
    });

    await expect(deliverVoiceRecording(new ArrayBuffer(4), "channel-1", 123)).rejects.toThrow(
      "attachment id",
    );
  });
});
