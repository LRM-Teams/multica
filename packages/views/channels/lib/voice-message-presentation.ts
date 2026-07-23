import type { Attachment, ChannelMessage, MessagePart } from "@multica/core/types";

type VoicePart = Extract<MessagePart, { type: "voice" }>;

export type VoiceMessagePresentation =
  | {
      voicePart: VoicePart;
      recordingAttachment: Attachment;
      consumedAttachmentIds: string[];
      source: "recording";
    }
  | {
      voicePart: VoicePart;
      consumedAttachmentIds: string[];
      source: "structured" | "legacy-agent-audio";
    };

export function voiceBubbleWidthPx(durationSeconds: number | null): number {
  if (!durationSeconds) return 112;
  return Math.min(240, 112 + Math.max(0, durationSeconds - 1) * 12);
}

/**
 * Resolve the message modality once for both the body and voice control.
 *
 * Structured `voice` remains authoritative. The second branch is deliberately
 * narrow: it covers the production shape emitted by runtimes that predate
 * `multica message send --voice`. Their uploaded bytes are only a modality
 * signal; playback is always synthesized from the canonical transcript.
 */
export function resolveVoiceMessagePresentation(
  message: ChannelMessage,
): VoiceMessagePresentation | null {
  const voicePart = message.parts?.find(
    (part): part is VoicePart => part.type === "voice",
  );
  if (voicePart) {
    const recordingAttachment = message.attachments?.find(
      (candidate) =>
        candidate.id === voicePart.attachment_id &&
        candidate.content_type.toLowerCase().startsWith("audio/"),
    );
    if (recordingAttachment) {
      return {
        voicePart,
        recordingAttachment,
        consumedAttachmentIds: [recordingAttachment.id],
        source: "recording",
      };
    }
    const legacyAudio = resolveLegacyAgentAudioAttachment(message, true);
    return {
      voicePart,
      consumedAttachmentIds: legacyAudio ? [legacyAudio.id] : [],
      source: "structured",
    };
  }

  const legacyAudio = resolveLegacyAgentAudioAttachment(message, false);
  if (!legacyAudio) return null;

  return {
    voicePart: { type: "voice" },
    consumedAttachmentIds: [legacyAudio.id],
    source: "legacy-agent-audio",
  };
}

function resolveLegacyAgentAudioAttachment(
  message: ChannelMessage,
  allowVoicePart: boolean,
) {
  if (message.type !== "agent" || !message.author_id || !message.content.trim()) return null;
  const parts = message.parts ?? [];
  const expectedPartCount = allowVoicePart ? 3 : 2;
  if (
    parts.length !== expectedPartCount ||
    parts.filter((part) => part.type === "text" && part.text.trim()).length !== 1 ||
    parts.filter((part) => part.type === "attachment").length !== 1 ||
    parts.filter((part) => part.type === "voice").length !== (allowVoicePart ? 1 : 0)
  ) {
    return null;
  }

  const attachmentPart = parts.find(
    (part): part is Extract<MessagePart, { type: "attachment" }> =>
      part.type === "attachment",
  );
  if (!attachmentPart) return null;
  const attachment = message.attachments?.find(
    (candidate) => candidate.id === attachmentPart.attachment_id,
  );
  if (
    !attachment ||
    attachment.uploader_type !== "agent" ||
    attachment.uploader_id !== message.author_id ||
    !attachment.content_type.toLowerCase().startsWith("audio/")
  ) {
    return null;
  }
  return attachment;
}
