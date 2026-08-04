import {
  VoiceCallMediaError,
  type VoiceCallMediaErrorCode,
} from "./volcengine-media-session";

export const DUPLEX_INPUT_SAMPLE_RATE = 16_000;
export const DUPLEX_OUTPUT_SAMPLE_RATE = 24_000;
export const DUPLEX_FRAME_MS = 100;
export const DUPLEX_FRAME_SAMPLES =
  (DUPLEX_INPUT_SAMPLE_RATE * DUPLEX_FRAME_MS) / 1_000;
export const DUPLEX_SILENCE_COMMIT_MS = 600;
export const DUPLEX_BARGE_IN_RMS = 0.015;
/** Louder threshold while speakerphone is on — speaker→mic echo otherwise self-interrupts TTS. */
export const DUPLEX_SPEAKERPHONE_BARGE_IN_RMS = 0.08;

export type DuplexToolStatus = "started" | "done" | "error";
export type DuplexAsrPhase = "started" | "completed";

export interface DuplexToolEvent {
  name: string;
  status: DuplexToolStatus;
  result?: string;
}

export interface DuplexServerEvent {
  type: string;
  call_id?: string;
  session_id?: string;
  transcript?: string;
  phase?: string;
  audio?: string;
  text?: string;
  name?: string;
  status?: string;
  result?: string;
  code?: string;
  message?: string;
  sample_rate?: number;
  audio_format?: string;
}

export interface DuplexMediaSessionEvents {
  onReady?: (sessionId: string) => void;
  onASR?: (phase: DuplexAsrPhase, transcript: string) => void;
  onTool?: (event: DuplexToolEvent) => void;
  onError?: (code: VoiceCallMediaErrorCode, message: string) => void;
  onClosed?: () => void;
  onPlaybackStarted?: () => void;
}

export interface DuplexMediaSession {
  connect(wsUrl: string, deviceId?: string): Promise<void>;
  setMuted(muted: boolean): void;
  setSpeakerphone(enabled: boolean): void;
  interrupt(): void;
  disconnect(): Promise<void>;
}

export type DuplexMediaSessionFactory = (
  events: DuplexMediaSessionEvents,
) => DuplexMediaSession;

export function encodePcmS16leToBase64(samples: Int16Array): string {
  const bytes = new Uint8Array(samples.buffer, samples.byteOffset, samples.byteLength);
  let binary = "";
  for (let i = 0; i < bytes.length; i += 1) {
    binary += String.fromCharCode(bytes[i]!);
  }
  return btoa(binary);
}

export function decodeBase64ToPcmS16le(base64: string): Int16Array {
  const binary = atob(base64);
  const bytes = new Uint8Array(binary.length);
  for (let i = 0; i < binary.length; i += 1) {
    bytes[i] = binary.charCodeAt(i);
  }
  return new Int16Array(bytes.buffer);
}

export function downsampleFloat32To16k(
  input: Float32Array,
  inputSampleRate: number,
): Int16Array {
  if (inputSampleRate === DUPLEX_INPUT_SAMPLE_RATE) {
    const out = new Int16Array(input.length);
    for (let i = 0; i < input.length; i += 1) {
      const clamped = Math.max(-1, Math.min(1, input[i] ?? 0));
      out[i] = clamped < 0 ? clamped * 0x8000 : clamped * 0x7fff;
    }
    return out;
  }

  const ratio = inputSampleRate / DUPLEX_INPUT_SAMPLE_RATE;
  const outputLength = Math.floor(input.length / ratio);
  const out = new Int16Array(outputLength);
  for (let i = 0; i < outputLength; i += 1) {
    const start = Math.floor(i * ratio);
    const end = Math.min(input.length, Math.floor((i + 1) * ratio));
    let sum = 0;
    for (let j = start; j < end; j += 1) {
      sum += input[j] ?? 0;
    }
    const avg = sum / Math.max(1, end - start);
    const clamped = Math.max(-1, Math.min(1, avg));
    out[i] = clamped < 0 ? clamped * 0x8000 : clamped * 0x7fff;
  }
  return out;
}

export function parseDuplexServerEvent(raw: unknown): DuplexServerEvent | null {
  if (!raw || typeof raw !== "object") return null;
  const event = raw as DuplexServerEvent;
  return typeof event.type === "string" ? event : null;
}

export function pcmS16leToFloat32(samples: Int16Array): Float32Array {
  const out = new Float32Array(samples.length);
  for (let i = 0; i < samples.length; i += 1) {
    out[i] = (samples[i] ?? 0) / 0x8000;
  }
  return out;
}

function rmsOfFloat32(input: Float32Array): number {
  if (input.length === 0) return 0;
  let sum = 0;
  for (let i = 0; i < input.length; i += 1) {
    const sample = input[i] ?? 0;
    sum += sample * sample;
  }
  return Math.sqrt(sum / input.length);
}

interface WebSocketLike {
  readyState: number;
  send(data: string): void;
  close(): void;
  addEventListener(
    type: "open" | "message" | "error" | "close",
    listener: (event: Event | MessageEvent<string>) => void,
  ): void;
  removeEventListener(
    type: "open" | "message" | "error" | "close",
    listener: (event: Event | MessageEvent<string>) => void,
  ): void;
}

type WebSocketConstructor = new (url: string) => WebSocketLike;

export interface DuplexMediaSessionDeps {
  WebSocket?: WebSocketConstructor;
  getUserMedia?: typeof navigator.mediaDevices.getUserMedia;
  AudioContext?: typeof AudioContext;
}

export function createDuplexMediaSession(
  events: DuplexMediaSessionEvents,
  deps: DuplexMediaSessionDeps = {},
): DuplexMediaSession {
  const WebSocketImpl = deps.WebSocket ?? globalThis.WebSocket;
  const getUserMedia = deps.getUserMedia
    ?? navigator.mediaDevices?.getUserMedia?.bind(navigator.mediaDevices);
  const AudioContextImpl = deps.AudioContext ?? globalThis.AudioContext;

  let ws: WebSocketLike | null = null;
  let captureContext: AudioContext | null = null;
  let playbackContext: AudioContext | null = null;
  let mediaStream: MediaStream | null = null;
  let processor: ScriptProcessorNode | null = null;
  let sourceNode: MediaStreamAudioSourceNode | null = null;
  let muted = false;
  let speakerphone = false;
  let closed = false;
  // A disconnect can race an awaited browser permission prompt. Incrementing
  // this generation invalidates every in-flight setup continuation, even if a
  // later connect has already reset `closed`.
  let connectionGeneration = 0;
  let pcmBuffer: number[] = [];
  let silenceMs = 0;
  let playbackTime = 0;
  let playingTts = false;
  const scheduledSources: AudioBufferSourceNode[] = [];

  const sendJson = (payload: Record<string, string>) => {
    if (!ws || ws.readyState !== 1) return;
    ws.send(JSON.stringify(payload));
  };

  const flushPcmFrame = (commitAfter = false) => {
    if (pcmBuffer.length === 0) {
      if (commitAfter) sendJson({ type: "client.audio.commit" });
      return;
    }
    const samples = Int16Array.from(pcmBuffer);
    pcmBuffer = [];
    sendJson({
      type: "client.audio.append",
      audio: encodePcmS16leToBase64(samples),
    });
    if (commitAfter) sendJson({ type: "client.audio.commit" });
  };

  const clearPlaybackQueue = () => {
    for (const node of scheduledSources.splice(0)) {
      try {
        node.stop();
      } catch {
        // already stopped
      }
    }
    if (playbackContext) {
      playbackTime = playbackContext.currentTime;
    }
    playingTts = false;
  };

  const ensureAudioContextsRunning = async () => {
    // Mobile browsers often leave freshly created AudioContexts suspended.
    // Ringback already resumes; Duplex must too or UI "connects" with silence.
    if (captureContext?.state === "suspended") {
      await captureContext.resume().catch(() => undefined);
    }
    if (playbackContext?.state === "suspended") {
      await playbackContext.resume().catch(() => undefined);
    }
  };

  const schedulePlayback = (samples: Int16Array, sampleRate: number) => {
    if (!playbackContext || samples.length === 0) return;
    if (playbackContext.state === "suspended") {
      void playbackContext.resume().catch(() => undefined);
    }
    const floatSamples = pcmS16leToFloat32(samples);
    const buffer = playbackContext.createBuffer(
      1,
      floatSamples.length,
      sampleRate,
    );
    // getChannelData().set avoids Float32Array<ArrayBufferLike> vs
    // Float32Array<ArrayBuffer> friction under newer lib DOM typings.
    buffer.getChannelData(0).set(floatSamples);
    const node = playbackContext.createBufferSource();
    node.buffer = buffer;
    node.connect(playbackContext.destination);
    const startAt = Math.max(playbackContext.currentTime, playbackTime);
    node.start(startAt);
    playbackTime = startAt + buffer.duration;
    scheduledSources.push(node);
    node.onended = () => {
      const index = scheduledSources.indexOf(node);
      if (index >= 0) scheduledSources.splice(index, 1);
      if (scheduledSources.length === 0) playingTts = false;
    };
    if (!playingTts) {
      playingTts = true;
      events.onPlaybackStarted?.();
    }
  };

  const applySpeakerphone = async () => {
    if (!playbackContext) return;
    const sinkAware = playbackContext as AudioContext & {
      setSinkId?: (sinkId: string) => Promise<void>;
    };
    if (typeof sinkAware.setSinkId !== "function") return;
    try {
      await sinkAware.setSinkId(speakerphone ? "default" : "");
    } catch {
      // Browser may not expose output routing; no-op.
    }
  };

  const handleServerEvent = (event: DuplexServerEvent) => {
    switch (event.type) {
      case "duplex.ready":
        events.onReady?.(event.session_id ?? "");
        break;
      case "duplex.asr": {
        const phase = event.phase === "completed" ? "completed" : "started";
        events.onASR?.(phase, event.transcript ?? "");
        break;
      }
      case "duplex.audio.delta":
        if (event.audio) {
          schedulePlayback(
            decodeBase64ToPcmS16le(event.audio),
            event.sample_rate ?? DUPLEX_OUTPUT_SAMPLE_RATE,
          );
        }
        break;
      case "duplex.tool": {
        const status = event.status;
        if (
          status === "started" ||
          status === "done" ||
          status === "error"
        ) {
          events.onTool?.({
            name: event.name ?? "",
            status,
            result: event.result,
          });
        }
        break;
      }
      case "duplex.error":
        events.onError?.(
          "provider_error",
          event.message ?? event.code ?? "Duplex media failed",
        );
        break;
      case "duplex.closed":
        events.onClosed?.();
        break;
      default:
        break;
    }
  };

  const teardownCapture = async () => {
    processor?.disconnect();
    sourceNode?.disconnect();
    processor = null;
    sourceNode = null;
    if (mediaStream) {
      for (const track of mediaStream.getTracks()) track.stop();
      mediaStream = null;
    }
    if (captureContext) {
      await captureContext.close().catch(() => undefined);
      captureContext = null;
    }
  };

  const teardownPlayback = async () => {
    clearPlaybackQueue();
    if (playbackContext) {
      await playbackContext.close().catch(() => undefined);
      playbackContext = null;
    }
  };

  return {
    async connect(wsUrl: string, deviceId?: string): Promise<void> {
      if (!WebSocketImpl) {
        throw new VoiceCallMediaError(
          "unsupported",
          "WebSocket is not available in this environment",
        );
      }
      if (!getUserMedia) {
        throw new VoiceCallMediaError(
          "permission_denied",
          "Microphone access is not available",
        );
      }
      if (!AudioContextImpl) {
        throw new VoiceCallMediaError(
          "unsupported",
          "Web Audio is not available in this environment",
        );
      }
      if (
        typeof globalThis.window !== "undefined" &&
        !globalThis.window.isSecureContext
      ) {
        throw new VoiceCallMediaError(
          "insecure_context",
          "Microphone access requires a secure context",
        );
      }

      const setupGeneration = ++connectionGeneration;
      closed = false;
      ws = new WebSocketImpl(wsUrl);

      await new Promise<void>((resolve, reject) => {
        const onOpen = () => {
          ws?.removeEventListener("open", onOpen);
          ws?.removeEventListener("error", onError);
          resolve();
        };
        const onError = () => {
          ws?.removeEventListener("open", onOpen);
          ws?.removeEventListener("error", onError);
          reject(new VoiceCallMediaError(
            "media_failed",
            "Duplex WebSocket connection failed",
          ));
        };
        ws?.addEventListener("open", onOpen);
        ws?.addEventListener("error", onError);
      });

      if (closed || setupGeneration !== connectionGeneration) return;

      ws.addEventListener("message", (raw) => {
        const message = raw as MessageEvent<string>;
        try {
          const parsed = parseDuplexServerEvent(JSON.parse(message.data));
          if (parsed) handleServerEvent(parsed);
        } catch {
          events.onError?.(
            "provider_error",
            "Invalid duplex server event",
          );
        }
      });

      ws.addEventListener("close", () => {
        if (!closed) events.onClosed?.();
      });

      captureContext = new AudioContextImpl();
      playbackContext = new AudioContextImpl();
      await ensureAudioContextsRunning();
      playbackTime = playbackContext.currentTime;
      await applySpeakerphone();

      const grantedStream = await getUserMedia({
        audio: deviceId ? { deviceId: { exact: deviceId } } : true,
        video: false,
      });
      if (closed || setupGeneration !== connectionGeneration) {
        for (const track of grantedStream.getTracks()) track.stop();
        return;
      }
      mediaStream = grantedStream;
      // Permission grant is a fresh user-activation window on mobile — resume again.
      await ensureAudioContextsRunning();
      if (closed || setupGeneration !== connectionGeneration) {
        await teardownCapture();
        await teardownPlayback();
        return;
      }
      sourceNode = captureContext.createMediaStreamSource(mediaStream);
      processor = captureContext.createScriptProcessor(4096, 1, 1);
      processor.onaudioprocess = (audioEvent) => {
        if (muted || closed) return;
        const channel = audioEvent.inputBuffer.getChannelData(0);
        const rms = rmsOfFloat32(channel);
        const downsampled = downsampleFloat32To16k(
          channel,
          captureContext?.sampleRate ?? DUPLEX_INPUT_SAMPLE_RATE,
        );
        for (let i = 0; i < downsampled.length; i += 1) {
          pcmBuffer.push(downsampled[i]!);
        }

        const bargeInRms = speakerphone
          ? DUPLEX_SPEAKERPHONE_BARGE_IN_RMS
          : DUPLEX_BARGE_IN_RMS;
        if (playingTts && rms >= bargeInRms) {
          sendJson({ type: "client.interrupt" });
          clearPlaybackQueue();
        }

        const frameSamples = DUPLEX_FRAME_SAMPLES;
        while (pcmBuffer.length >= frameSamples) {
          const frame = pcmBuffer.splice(0, frameSamples);
          sendJson({
            type: "client.audio.append",
            audio: encodePcmS16leToBase64(Int16Array.from(frame)),
          });
        }

        if (rms < 0.004) {
          silenceMs += (channel.length / (captureContext?.sampleRate ?? 48_000)) * 1_000;
          if (silenceMs >= DUPLEX_SILENCE_COMMIT_MS && pcmBuffer.length > 0) {
            flushPcmFrame(true);
            silenceMs = 0;
          }
        } else {
          silenceMs = 0;
        }
      };
      sourceNode.connect(processor);
      // Keep ScriptProcessor alive without monitoring the mic on the speaker
      // (open destination would feed speakerphone echo → false barge-in).
      const silentGain = captureContext.createGain();
      silentGain.gain.value = 0;
      processor.connect(silentGain);
      silentGain.connect(captureContext.destination);
    },

    setMuted(nextMuted: boolean) {
      muted = nextMuted;
    },

    setSpeakerphone(enabled: boolean) {
      speakerphone = enabled;
      void applySpeakerphone();
    },

    interrupt() {
      sendJson({ type: "client.interrupt" });
      clearPlaybackQueue();
    },

    async disconnect(): Promise<void> {
      if (closed) return;
      closed = true;
      connectionGeneration += 1;
      flushPcmFrame(false);
      sendJson({ type: "client.close" });
      ws?.close();
      ws = null;
      await teardownCapture();
      await teardownPlayback();
    },
  };
}
