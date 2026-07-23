import { api } from "@multica/core/api";

const AUTOPLAY_WINDOW_MS = 30 * 60 * 1000;
const CREATED_AT_CLOCK_SKEW_MS = 30_000;

type PendingVoiceScope = {
  preparedAt: number;
};

export type VoicePlayback = {
  durationMs: number;
  finished: Promise<void>;
  stop: () => void;
};

export type PreparedVoiceAudio = {
  audio: ArrayBuffer;
  durationMs: number | null;
};

let audioContext: AudioContext | null = null;
let activeSource: AudioBufferSourceNode | null = null;
const pendingScopes = new Map<string, PendingVoiceScope>();
const claimedAutoplayMessages = new Map<string, number>();
const preparedVoiceAudio = new Map<string, Promise<PreparedVoiceAudio>>();
const MAX_PREPARED_VOICE_AUDIO = 8;

export function voicePlaybackScope(channelId: string, threadRootMessageId?: string | null): string {
  return `${channelId}:${threadRootMessageId ?? "main"}`;
}

function getAudioContext(): AudioContext {
  if (typeof window === "undefined" || typeof AudioContext === "undefined") {
    throw new Error("audio playback is not supported");
  }
  audioContext ??= new AudioContext();
  return audioContext;
}

function pruneVoicePlaybackState(now: number): void {
  for (const [scope, pending] of pendingScopes) {
    if (now - pending.preparedAt > AUTOPLAY_WINDOW_MS) pendingScopes.delete(scope);
  }
  for (const [messageId, claimedAt] of claimedAutoplayMessages) {
    if (now - claimedAt > AUTOPLAY_WINDOW_MS) claimedAutoplayMessages.delete(messageId);
  }
}

/** Call from a user send gesture so a later asynchronous Agent reply can speak. */
export function prepareVoicePlayback(scope: string): void {
  if (!scope) return;
  const now = Date.now();
  pruneVoicePlaybackState(now);
  pendingScopes.set(scope, { preparedAt: now });
  try {
    const context = getAudioContext();
    void context.resume();
  } catch {
    // The message still sends. Its explicit play button reports unsupported playback.
  }
}

export function cancelVoicePlayback(scope: string): void {
  pendingScopes.delete(scope);
}

export function claimVoiceAutoplay(
  messageId: string,
  scope: string,
  createdAt: string,
): boolean {
  if (!messageId) return false;
  const now = Date.now();
  pruneVoicePlaybackState(now);
  if (claimedAutoplayMessages.has(messageId)) return false;
  const pending = pendingScopes.get(scope);
  if (!pending) return false;
  const messageTime = Date.parse(createdAt);
  if (!Number.isFinite(messageTime) || messageTime < pending.preparedAt - CREATED_AT_CLOCK_SKEW_MS) {
    return false;
  }
  pendingScopes.delete(scope);
  claimedAutoplayMessages.set(messageId, now);
  return true;
}

/** Fetch voice bytes without playing them so the bubble can show real duration. */
export function prepareVoiceAudio(text: string): Promise<PreparedVoiceAudio> {
  const transcript = text.trim();
  if (!transcript) return Promise.reject(new Error("voice transcript is required"));
  const cached = preparedVoiceAudio.get(transcript);
  if (cached) return cached;

  const pending = api.synthesizeVoice(transcript).catch((error: unknown) => {
    preparedVoiceAudio.delete(transcript);
    throw error;
  });
  preparedVoiceAudio.set(transcript, pending);
  if (preparedVoiceAudio.size > MAX_PREPARED_VOICE_AUDIO) {
    const oldest = preparedVoiceAudio.keys().next().value as string | undefined;
    if (oldest && oldest !== transcript) preparedVoiceAudio.delete(oldest);
  }
  return pending;
}

export async function startPreparedVoicePlayback(
  prepared: PreparedVoiceAudio,
): Promise<VoicePlayback> {
  const context = getAudioContext();
  await context.resume();
  const decoded = await context.decodeAudioData(prepared.audio.slice(0));

  activeSource?.stop();
  const source = context.createBufferSource();
  source.buffer = decoded;
  source.connect(context.destination);
  activeSource = source;

  let settle: (() => void) | null = null;
  const finished = new Promise<void>((resolve) => {
    settle = resolve;
  });
  source.onended = () => {
    if (activeSource === source) activeSource = null;
    settle?.();
    settle = null;
    source.disconnect();
  };
  source.start();

  return {
    durationMs: Math.max(0, Math.round(decoded.duration * 1000)),
    finished,
    stop: () => {
      if (activeSource === source) source.stop();
    },
  };
}

export async function startVoicePlayback(text: string): Promise<VoicePlayback> {
  return startPreparedVoicePlayback(await prepareVoiceAudio(text));
}
