import { api } from "@multica/core/api";
import type { Attachment } from "@multica/core/types";
import { encodeVoiceWAV } from "./voice-audio";

export type VoiceRecordingDelivery = {
  attachment: Attachment;
};

/**
 * Persist the decoded waveform before message send. Transcription belongs to
 * the server after the attachment is atomically bound to its message.
 */
export async function deliverVoiceRecording(
  pcm: ArrayBuffer,
  channelId: string,
  recordedAtMs = Date.now(),
): Promise<VoiceRecordingDelivery> {
  const wav = encodeVoiceWAV(pcm);
  const file = new File([wav], `voice-${recordedAtMs}.wav`, { type: "audio/wav" });
  const attachment = await api.uploadFile(file, { channelId });
  if (!attachment.id) {
    throw new Error("voice upload did not return an attachment id");
  }
  return { attachment };
}
