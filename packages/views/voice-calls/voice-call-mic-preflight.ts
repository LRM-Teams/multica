import { VoiceCallMediaError } from "./volcengine-media-session";

/**
 * Acquire (and immediately release) the microphone while the caller's click
 * gesture is still active. Mobile Chromium often drops transient activation
 * across later `await createCall` / provider hops, which then surfaces as a
 * generic media_failed / "check microphone" failure.
 */
export async function preflightMicrophoneAccess(
  deviceId?: string,
): Promise<void> {
  if (globalThis.isSecureContext === false) {
    throw new VoiceCallMediaError(
      "insecure_context",
      "Voice calls require a secure HTTPS context",
    );
  }

  const getUserMedia = globalThis.navigator?.mediaDevices?.getUserMedia?.bind(
    globalThis.navigator.mediaDevices,
  );
  if (!getUserMedia) {
    throw new VoiceCallMediaError(
      "microphone_unavailable",
      "Microphone access is not available",
    );
  }

  let stream: MediaStream;
  try {
    stream = await getUserMedia({
      audio: deviceId ? { deviceId: { exact: deviceId } } : true,
      video: false,
    });
  } catch (error) {
    if (error instanceof VoiceCallMediaError) throw error;
    const denied = error instanceof DOMException &&
      (error.name === "NotAllowedError" || error.name === "PermissionDeniedError");
    throw new VoiceCallMediaError(
      denied ? "permission_denied" : "microphone_unavailable",
      denied
        ? "Microphone permission was denied"
        : "Failed to start microphone capture",
      undefined,
      { cause: error },
    );
  }

  for (const track of stream.getTracks()) {
    track.stop();
  }
}
