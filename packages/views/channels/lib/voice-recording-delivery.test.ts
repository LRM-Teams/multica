// @vitest-environment node
import { beforeEach, describe, expect, it, vi } from "vitest";
import { deliverVoiceRecording } from "./voice-recording-delivery";

const apiMocks = vi.hoisted(() => ({
  uploadFile: vi.fn(),
}));

vi.mock("@multica/core/api", () => ({ api: apiMocks }));

describe("deliverVoiceRecording", () => {
  beforeEach(() => {
    apiMocks.uploadFile.mockReset().mockResolvedValue({
      id: "recording-1",
      filename: "voice-recording.wav",
      content_type: "audio/wav",
      size_bytes: 48,
    });
  });

  it("uploads the WAV without blocking message send on browser ASR", async () => {
    const pending = deliverVoiceRecording(new ArrayBuffer(4), "channel-1", 123);

    expect(apiMocks.uploadFile).toHaveBeenCalledWith(
      expect.objectContaining({ name: "voice-123.wav", type: "audio/wav" }),
      { channelId: "channel-1" },
    );

    await expect(pending).resolves.toEqual({
      attachment: expect.objectContaining({ id: "recording-1" }),
    });
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
