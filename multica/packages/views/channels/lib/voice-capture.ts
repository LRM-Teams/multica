export type VoiceCaptureUnavailableReason = "insecure-context" | "unsupported";

export type VoiceCaptureCapabilities = {
  secureContext: boolean;
  hasGetUserMedia: boolean;
  hasMediaRecorder: boolean;
  hasAudioContext: boolean;
};

export function voiceCaptureUnavailableReason(
  capabilities: VoiceCaptureCapabilities = {
    secureContext: typeof window !== "undefined" && window.isSecureContext,
    hasGetUserMedia: typeof navigator !== "undefined" && Boolean(navigator.mediaDevices?.getUserMedia),
    hasMediaRecorder: typeof MediaRecorder !== "undefined",
    hasAudioContext: typeof AudioContext !== "undefined",
  },
): VoiceCaptureUnavailableReason | null {
  if (!capabilities.secureContext) return "insecure-context";
  if (!capabilities.hasGetUserMedia || !capabilities.hasMediaRecorder || !capabilities.hasAudioContext) {
    return "unsupported";
  }
  return null;
}
