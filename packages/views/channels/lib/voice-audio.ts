import type { Attachment, MessagePart } from "@multica/core/types";

export const VOICE_SAMPLE_RATE = 16_000;
export const MAX_VOICE_RECORDING_MS = 60_000;

export type VoiceRecordingAttachment = Pick<
  Attachment,
  "id" | "filename" | "content_type" | "size_bytes"
>;

export function buildRecordedVoiceMessageParts(
  durationMs: number,
  recording: VoiceRecordingAttachment,
): MessagePart[] {
  return [
    {
      type: "voice",
      duration_ms: Math.max(0, Math.min(MAX_VOICE_RECORDING_MS, Math.round(durationMs))),
      attachment_id: recording.id,
      filename: recording.filename,
      content_type: recording.content_type,
      size_bytes: recording.size_bytes,
    },
  ];
}

/** Wrap backend-compatible PCM in a browser-playable 16 kHz mono PCM WAV. */
export function encodeVoiceWAV(pcm: ArrayBuffer): ArrayBuffer {
  const headerSize = 44;
  const wav = new ArrayBuffer(headerSize + pcm.byteLength);
  const bytes = new Uint8Array(wav);
  const view = new DataView(wav);
  const writeASCII = (offset: number, value: string) => {
    for (let index = 0; index < value.length; index += 1) {
      bytes[offset + index] = value.charCodeAt(index);
    }
  };

  writeASCII(0, "RIFF");
  view.setUint32(4, wav.byteLength - 8, true);
  writeASCII(8, "WAVE");
  writeASCII(12, "fmt ");
  view.setUint32(16, 16, true);
  view.setUint16(20, 1, true);
  view.setUint16(22, 1, true);
  view.setUint32(24, VOICE_SAMPLE_RATE, true);
  view.setUint32(28, VOICE_SAMPLE_RATE * 2, true);
  view.setUint16(32, 2, true);
  view.setUint16(34, 16, true);
  writeASCII(36, "data");
  view.setUint32(40, pcm.byteLength, true);
  bytes.set(new Uint8Array(pcm), headerSize);
  return wav;
}

/** Downmix every decoded channel into one mono track without clipping the sum. */
export function downmixAudioBuffer(buffer: AudioBuffer): Float32Array {
  const mono = new Float32Array(buffer.length);
  if (buffer.numberOfChannels === 0) return mono;
  for (let channel = 0; channel < buffer.numberOfChannels; channel += 1) {
    const samples = buffer.getChannelData(channel);
    for (let index = 0; index < mono.length; index += 1) {
      mono[index] = (mono[index] ?? 0) + (samples[index] ?? 0) / buffer.numberOfChannels;
    }
  }
  return mono;
}

/**
 * Resample decoded browser audio and encode the backend's exact ASR contract:
 * signed 16-bit little-endian mono PCM at 16 kHz.
 */
export function encodeVoicePCM(samples: Float32Array, sourceSampleRate: number): ArrayBuffer {
  if (!Number.isFinite(sourceSampleRate) || sourceSampleRate <= 0) {
    throw new Error("invalid source sample rate");
  }
  if (samples.length === 0) return new ArrayBuffer(0);

  const outputLength = Math.max(
    1,
    Math.round(samples.length * (VOICE_SAMPLE_RATE / sourceSampleRate)),
  );
  const pcm = new ArrayBuffer(outputLength * 2);
  const view = new DataView(pcm);
  const sourceStep = sourceSampleRate / VOICE_SAMPLE_RATE;

  for (let outputIndex = 0; outputIndex < outputLength; outputIndex += 1) {
    const sourcePosition = outputIndex * sourceStep;
    const leftIndex = Math.min(samples.length - 1, Math.floor(sourcePosition));
    const rightIndex = Math.min(samples.length - 1, leftIndex + 1);
    const fraction = sourcePosition - leftIndex;
    const interpolated =
      (samples[leftIndex] ?? 0) * (1 - fraction) + (samples[rightIndex] ?? 0) * fraction;
    const clamped = Math.max(-1, Math.min(1, interpolated));
    const sample = clamped < 0 ? Math.round(clamped * 0x8000) : Math.round(clamped * 0x7fff);
    view.setInt16(outputIndex * 2, sample, true);
  }

  return pcm;
}
