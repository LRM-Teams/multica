import { api } from "@multica/core/api";
import type { Attachment } from "@multica/core/types";
import { encodeVoiceWAV } from "./voice-audio";

export type VoiceRecordingDelivery = {
  transcript: string;
  attachment: Attachment;
};

/**
 * Persist the same decoded waveform that ASR reads. Upload and transcription
 * run together; if transcription fails after upload, the unbound row is
 * deleted so a failed recording does not leave an orphan attachment.
 */
export async function deliverVoiceRecording(
  pcm: ArrayBuffer,
  channelId: string,
  recordedAtMs = Date.now(),
): Promise<VoiceRecordingDelivery> {
  const wav = encodeVoiceWAV(pcm);
  const file = new File([wav], `voice-${recordedAtMs}.wav`, { type: "audio/wav" });
  const upload = api.uploadFile(file, { channelId });

  try {
    const [transcript, attachment] = await Promise.all([
      api.transcribeVoice(pcm),
      upload,
    ]);
    if (!attachment.id) {
      throw new Error("voice upload did not return an attachment id");
    }
    return { transcript, attachment };
  } catch (error) {
    const attachment = await upload.catch(() => null);
    if (attachment?.id) {
      await api.deleteAttachment(attachment.id).catch(() => undefined);
    }
    throw error;
  }
}
