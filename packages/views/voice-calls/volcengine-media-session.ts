import type { VoiceCallMedia } from "@multica/core/types";

export type VoiceCallMediaState =
  | "idle"
  | "joining"
  | "connected"
  | "muted"
  | "reconnecting"
  | "failed"
  | "closed";

export type VoiceCallMediaErrorCode =
  | "already_started"
  | "insecure_context"
  | "unsupported"
  | "sdk_unavailable"
  | "cancelled"
  | "join_failed"
  | "microphone_unavailable"
  | "permission_denied"
  | "publish_failed"
  | "mute_failed"
  | "unmute_failed"
  | "playback_failed"
  | "media_failed"
  | "provider_error"
  | "cleanup_failed";

export class VoiceCallMediaError extends Error {
  constructor(
    public readonly code: VoiceCallMediaErrorCode,
    message: string,
    public readonly providerCode?: string,
    options?: ErrorOptions,
  ) {
    super(message, options);
    this.name = "VoiceCallMediaError";
  }
}

export interface VoiceCallMediaEvents {
  onStateChange?: (state: VoiceCallMediaState) => void;
  onRemoteAudioStarted?: (remoteUserId: string) => void;
  onAutoplayBlocked?: (remoteUserId: string) => void;
  onError?: (error: VoiceCallMediaError) => void;
}

type DriverConnectionState = "connected" | "reconnecting";

export interface VoiceCallRTCEngine {
  join(media: VoiceCallMedia): Promise<void>;
  leave(): Promise<void>;
  startAudioCapture(deviceId?: string): Promise<void>;
  stopAudioCapture(): Promise<void>;
  publishAudio(): Promise<void>;
  unpublishAudio(): Promise<void>;
  getRemoteAudioTrack(userId: string): MediaStreamTrack | undefined;
  destroy(): void;
}

export interface VoiceCallRemoteAudioOutput {
  play(track: MediaStreamTrack): Promise<void>;
  stop(): void;
}

export type VoiceCallRemoteAudioOutputFactory =
  () => VoiceCallRemoteAudioOutput;

export interface VoiceCallRTCDriver {
  isSupported(): Promise<boolean>;
  createEngine(
    appId: string,
    callbacks: {
      onConnectionState: (state: DriverConnectionState) => void;
      onRemoteAudioStarted: (remoteUserId: string) => void;
      onAutoplayBlocked: (remoteUserId: string) => void;
      onFatalError: (providerCode: string) => void;
    },
  ): VoiceCallRTCEngine;
}

export type VoiceCallRTCDriverLoader = () => Promise<VoiceCallRTCDriver>;

async function loadVolcengineRTCDriver(): Promise<VoiceCallRTCDriver> {
  const rtc = await import("@volcengine/rtc");

  return {
    isSupported: () => rtc.default.isSupported(),
    createEngine: (appId, callbacks) => {
      const engine = rtc.default.createEngine(appId, {
        autoPlayPolicy: rtc.RTCAutoPlayPolicy.PLAY_MANUALLY,
      });

      engine.on(rtc.default.events.onConnectionStateChanged, ({ state }) => {
        switch (state) {
          case rtc.ConnectionState.CONNECTION_STATE_CONNECTED:
          case rtc.ConnectionState.CONNECTION_STATE_RECONNECTED:
            callbacks.onConnectionState("connected");
            break;
          case rtc.ConnectionState.CONNECTION_STATE_DISCONNECTED:
          case rtc.ConnectionState.CONNECTION_STATE_RECONNECTING:
          case rtc.ConnectionState.CONNECTION_STATE_LOST:
            callbacks.onConnectionState("reconnecting");
            break;
        }
      });
      engine.on(rtc.default.events.onAutoplayFailed, (event) => {
        if (event.kind === "audio" && event.userId) {
          callbacks.onAutoplayBlocked(event.userId);
        }
      });
      engine.on(rtc.default.events.onRemoteAudioFirstFrame, (event) => {
        if (event.userId) {
          callbacks.onRemoteAudioStarted(event.userId);
        }
      });
      engine.on(rtc.default.events.onError, (event) => {
        callbacks.onFatalError(String(event.errorCode));
      });

      return {
        join: (media) =>
          engine.joinRoom(
            media.token,
            media.room_id,
            { userId: media.user_id },
            {
              isAutoPublish: false,
              isAutoSubscribeAudio: true,
              isAutoSubscribeVideo: false,
            },
          ),
        leave: () => engine.leaveRoom(),
        startAudioCapture: async (deviceId) => {
          await engine.startAudioCapture(deviceId);
        },
        stopAudioCapture: () => engine.stopAudioCapture(),
        publishAudio: () => engine.publishStream(rtc.MediaType.AUDIO),
        unpublishAudio: () => engine.unpublishStream(rtc.MediaType.AUDIO),
        getRemoteAudioTrack: (userId) =>
          engine.getRemoteStreamTrack(
            userId,
            rtc.StreamIndex.STREAM_INDEX_MAIN,
            "audio",
          ),
        destroy: () => {
          engine.removeAllListeners();
          rtc.default.destroyEngine(engine);
        },
      };
    },
  };
}

class BrowserVoiceCallRemoteAudioOutput
  implements VoiceCallRemoteAudioOutput {
  private audio: HTMLAudioElement | null = null;

  async play(track: MediaStreamTrack): Promise<void> {
    if (
      typeof Audio === "undefined" ||
      typeof MediaStream === "undefined"
    ) {
      throw new VoiceCallMediaError(
        "playback_failed",
        "Browser audio output is unavailable",
      );
    }
    const audio = this.audio ?? new Audio();
    this.audio = audio;
    audio.autoplay = true;
    audio.muted = false;
    audio.volume = 1;
    audio.srcObject = new MediaStream([track]);
    await audio.play();
  }

  stop(): void {
    const audio = this.audio;
    this.audio = null;
    if (!audio) return;
    audio.pause();
    audio.srcObject = null;
  }
}

function createBrowserVoiceCallRemoteAudioOutput():
  VoiceCallRemoteAudioOutput {
  return new BrowserVoiceCallRemoteAudioOutput();
}

function mediaError(
  code: VoiceCallMediaErrorCode,
  message: string,
  cause?: unknown,
): VoiceCallMediaError {
  return cause instanceof VoiceCallMediaError
    ? cause
    : new VoiceCallMediaError(
      code,
      message,
      providerCodeFromCause(cause),
      { cause },
    );
}

function normalizeProviderCode(providerCode: unknown): string | undefined {
  if (typeof providerCode !== "string" && typeof providerCode !== "number") {
    return undefined;
  }
  const value = String(providerCode).trim();
  return /^-?\d{1,10}$/.test(value) ||
      /^[A-Z][A-Z0-9_]{0,63}$/.test(value)
    ? value
    : undefined;
}

function providerCodeFromCause(cause: unknown): string | undefined {
  if (typeof cause !== "object" || cause === null || !("code" in cause)) {
    return undefined;
  }
  return normalizeProviderCode(cause.code);
}

export class VolcengineVoiceMediaSession {
  private state: VoiceCallMediaState = "idle";
  private engine: VoiceCallRTCEngine | null = null;
  private joined = false;
  private capturing = false;
  private published = false;
  private muted = false;
  private ready = false;
  private captureDeviceId: string | undefined;
  private disconnectRequested = false;
  private fatalError: VoiceCallMediaError | null = null;
  private connectPromise: Promise<void> | null = null;
  private cleanupPromise: Promise<void> | null = null;
  private expectedRemoteUserId = "";
  private remoteAudioOutput: VoiceCallRemoteAudioOutput | null = null;
  private readonly remoteAudioFirstFrameUsers = new Set<string>();
  private readonly remoteAudioPlaybackConfirmedUsers = new Set<string>();
  private readonly remoteAudioAnswerReportedUsers = new Set<string>();
  private readonly remoteAudioPlaybackAttempts = new Map<
    string,
    Promise<void>
  >();

  constructor(
    private readonly events: VoiceCallMediaEvents = {},
    private readonly loadDriver: VoiceCallRTCDriverLoader =
      loadVolcengineRTCDriver,
    private readonly createRemoteAudioOutput:
      VoiceCallRemoteAudioOutputFactory =
      createBrowserVoiceCallRemoteAudioOutput,
  ) {}

  getState(): VoiceCallMediaState {
    return this.state;
  }

  connect(media: VoiceCallMedia, deviceId?: string): Promise<void> {
    if (this.state !== "idle") {
      return Promise.reject(
        new VoiceCallMediaError(
          "already_started",
          "Voice call media session has already started",
        ),
      );
    }
    this.setState("joining");
    this.connectPromise = this.connectInternal(media, deviceId);
    return this.connectPromise.finally(() => {
      this.connectPromise = null;
    });
  }

  private async connectInternal(
    media: VoiceCallMedia,
    deviceId?: string,
  ): Promise<void> {
    try {
      if (globalThis.isSecureContext === false) {
        throw new VoiceCallMediaError(
          "insecure_context",
          "Voice calls require a secure HTTPS context",
        );
      }
      this.expectedRemoteUserId = expectedVoiceAgentUserId(media.user_id);

      let driver: VoiceCallRTCDriver;
      try {
        driver = await this.loadDriver();
      } catch (error) {
        throw mediaError(
          "sdk_unavailable",
          "Failed to load the voice call media provider",
          error,
        );
      }
      this.throwIfDisconnectRequested();
      let supported: boolean;
      try {
        supported = await driver.isSupported();
      } catch (error) {
        throw mediaError(
          "sdk_unavailable",
          "Failed to check voice call media support",
          error,
        );
      }
      if (!supported) {
        throw new VoiceCallMediaError(
          "unsupported",
          "This browser does not support real-time voice calls",
        );
      }
      this.throwIfDisconnectRequested();

      try {
        this.engine = driver.createEngine(media.app_id, {
          onConnectionState: (state) => {
            if (
              !this.ready ||
              this.state === "closed" ||
              this.state === "failed"
            ) {
              return;
            }
            this.setState(
              state === "reconnecting"
                ? "reconnecting"
                : this.muted
                  ? "muted"
                  : "connected",
            );
          },
          onAutoplayBlocked: (remoteUserId) => {
            const normalizedUserId = remoteUserId.trim();
            if (normalizedUserId === this.expectedRemoteUserId) {
              this.events.onAutoplayBlocked?.(normalizedUserId);
            }
          },
          onRemoteAudioStarted: (remoteUserId) => {
            if (this.state === "closed" || this.state === "failed") return;
            const normalizedUserId = remoteUserId.trim();
            if (
              !normalizedUserId ||
              normalizedUserId !== this.expectedRemoteUserId
            ) {
              return;
            }
            this.remoteAudioFirstFrameUsers.add(normalizedUserId);
            if (
              this.remoteAudioPlaybackConfirmedUsers.has(normalizedUserId)
            ) {
              this.reportRemoteAudioStarted(normalizedUserId);
              return;
            }
            void this.startRemoteAudioPlayback(normalizedUserId).catch(() => {
              if (this.state === "closed" || this.state === "failed") return;
              this.events.onAutoplayBlocked?.(normalizedUserId);
            });
          },
          onFatalError: (providerCode) => {
            if (this.state === "closed" || this.state === "failed") return;
            const error = new VoiceCallMediaError(
              "provider_error",
              `Voice call media provider failed (${providerCode})`,
              normalizeProviderCode(providerCode),
            );
            this.fatalError = error;
            this.disconnectRequested = true;
            this.setState("failed");
            this.events.onError?.(error);
            queueMicrotask(() => {
              void this.cleanup().catch(() => undefined);
            });
          },
        });
      } catch (error) {
        throw mediaError(
          "sdk_unavailable",
          "Failed to initialize the voice call media provider",
          error,
        );
      }

      try {
        await this.engine.join(media);
        this.joined = true;
      } catch (error) {
        throw mediaError("join_failed", "Failed to join the voice room", error);
      }
      this.throwIfDisconnectRequested();

      try {
        this.captureDeviceId = deviceId;
        await this.engine.startAudioCapture(deviceId);
        this.capturing = true;
      } catch (error) {
        throw mediaError(
          "microphone_unavailable",
          "Failed to start microphone capture",
          error,
        );
      }
      this.throwIfDisconnectRequested();

      try {
        await this.engine.publishAudio();
        this.published = true;
      } catch (error) {
        throw mediaError(
          "publish_failed",
          "Failed to publish microphone audio",
          error,
        );
      }
      this.throwIfDisconnectRequested();
      this.ready = true;
      this.setState("connected");
    } catch (error) {
      const normalized = mediaError(
        "join_failed",
        "Failed to start voice call media",
        error,
      );
      await this.cleanup().catch(() => undefined);
      if (normalized.code === "cancelled") {
        this.setState("closed");
      } else {
        this.setState("failed");
        if (normalized !== this.fatalError) {
          this.events.onError?.(normalized);
        }
      }
      throw normalized;
    }
  }

  async setMuted(muted: boolean): Promise<void> {
    if (muted === this.muted) return;
    if (!this.engine || (this.state !== "connected" && this.state !== "muted")) {
      throw new VoiceCallMediaError(
        muted ? "mute_failed" : "unmute_failed",
        "Voice call media is not connected",
      );
    }

    if (muted) {
      let firstError: unknown;
      let audioBlocked = false;
      if (this.published) {
        try {
          await this.engine.unpublishAudio();
          this.published = false;
          audioBlocked = true;
        } catch (error) {
          firstError = error;
        }
      }
      if (this.capturing) {
        try {
          await this.engine.stopAudioCapture();
          this.capturing = false;
          audioBlocked = true;
        } catch (error) {
          firstError ??= error;
        }
      }
      if (!audioBlocked) {
        throw mediaError(
          "mute_failed",
          "Failed to stop outgoing microphone audio",
          firstError,
        );
      }
      this.muted = true;
      this.setState("muted");
      if (firstError) {
        throw mediaError(
          "mute_failed",
          "Microphone audio stopped, but media cleanup was incomplete",
          firstError,
        );
      }
      return;
    }

    let startedCapture = false;
    try {
      if (!this.capturing) {
        await this.engine.startAudioCapture(this.captureDeviceId);
        this.capturing = true;
        startedCapture = true;
      }
      if (!this.published) {
        await this.engine.publishAudio();
        this.published = true;
      }
    } catch (error) {
      if (startedCapture) {
        await this.engine.stopAudioCapture().catch(() => undefined);
        this.capturing = false;
      }
      throw mediaError(
        "unmute_failed",
        "Failed to resume outgoing microphone audio",
        error,
      );
    }
    this.muted = false;
    this.setState("connected");
  }

  async resumeRemoteAudio(remoteUserId: string): Promise<void> {
    const normalizedUserId = remoteUserId.trim();
    if (
      !this.engine ||
      !normalizedUserId ||
      normalizedUserId !== this.expectedRemoteUserId
    ) {
      throw new VoiceCallMediaError(
        "playback_failed",
        "Remote voice stream is not available",
      );
    }
    try {
      await this.startRemoteAudioPlayback(normalizedUserId);
    } catch (error) {
      throw mediaError(
        "playback_failed",
        "Failed to resume remote voice playback",
        error,
      );
    }
  }

  private startRemoteAudioPlayback(remoteUserId: string): Promise<void> {
    if (this.remoteAudioPlaybackConfirmedUsers.has(remoteUserId)) {
      this.reportRemoteAudioStarted(remoteUserId);
      return Promise.resolve();
    }
    const currentAttempt = this.remoteAudioPlaybackAttempts.get(remoteUserId);
    if (currentAttempt) return currentAttempt;

    const engine = this.engine;
    if (!engine) {
      return Promise.reject(
        new VoiceCallMediaError(
          "playback_failed",
          "Remote voice stream is not available",
        ),
      );
    }
    const track = engine.getRemoteAudioTrack(remoteUserId);
    if (!track) {
      return Promise.reject(
        new VoiceCallMediaError(
          "playback_failed",
          "Remote voice stream is not available",
        ),
      );
    }
    const output = this.remoteAudioOutput ?? this.createRemoteAudioOutput();
    this.remoteAudioOutput = output;

    const attempt = output.play(track)
      .then(() => {
        if (
          this.engine !== engine ||
          this.state === "closed" ||
          this.state === "failed"
        ) {
          return;
        }
        this.remoteAudioPlaybackConfirmedUsers.add(remoteUserId);
        this.reportRemoteAudioStarted(remoteUserId);
      })
      .finally(() => {
        if (this.remoteAudioPlaybackAttempts.get(remoteUserId) === attempt) {
          this.remoteAudioPlaybackAttempts.delete(remoteUserId);
        }
      });
    this.remoteAudioPlaybackAttempts.set(remoteUserId, attempt);
    return attempt;
  }

  private reportRemoteAudioStarted(remoteUserId: string): void {
    if (
      !this.remoteAudioFirstFrameUsers.has(remoteUserId) ||
      this.remoteAudioAnswerReportedUsers.has(remoteUserId)
    ) {
      return;
    }
    this.remoteAudioAnswerReportedUsers.add(remoteUserId);
    this.events.onRemoteAudioStarted?.(remoteUserId);
  }

  async disconnect(): Promise<void> {
    if (this.state === "closed") return;
    this.disconnectRequested = true;
    await this.connectPromise?.catch(() => undefined);
    try {
      await this.cleanup();
    } catch (error) {
      const normalized = mediaError(
        "cleanup_failed",
        "Voice call media cleanup was incomplete",
        error,
      );
      this.events.onError?.(normalized);
      this.setState("closed");
      throw normalized;
    }
    this.setState("closed");
  }

  private cleanup(): Promise<void> {
    if (this.cleanupPromise) return this.cleanupPromise;
    this.cleanupPromise = this.cleanupInternal();
    return this.cleanupPromise.finally(() => {
      this.cleanupPromise = null;
    });
  }

  private async cleanupInternal(): Promise<void> {
    const engine = this.engine;
    if (!engine) return;
    let firstError: unknown;

    if (this.capturing) {
      try {
        await engine.stopAudioCapture();
      } catch (error) {
        firstError = error;
      }
      this.capturing = false;
    }
    if (this.published) {
      try {
        await engine.unpublishAudio();
      } catch (error) {
        firstError ??= error;
      }
      this.published = false;
    }
    if (this.joined) {
      try {
        await engine.leave();
      } catch (error) {
        firstError ??= error;
      }
      this.joined = false;
    }

    try {
      this.remoteAudioOutput?.stop();
    } catch (error) {
      firstError ??= error;
    }
    try {
      engine.destroy();
    } catch (error) {
      firstError ??= error;
    }
    this.engine = null;
    this.remoteAudioOutput = null;
    this.expectedRemoteUserId = "";
    this.ready = false;
    this.remoteAudioFirstFrameUsers.clear();
    this.remoteAudioPlaybackConfirmedUsers.clear();
    this.remoteAudioAnswerReportedUsers.clear();
    this.remoteAudioPlaybackAttempts.clear();
    if (firstError) throw firstError;
  }

  private throwIfDisconnectRequested(): void {
    if (this.disconnectRequested) {
      if (this.fatalError) throw this.fatalError;
      throw new VoiceCallMediaError(
        "cancelled",
        "Voice call media startup was cancelled",
      );
    }
  }

  private setState(state: VoiceCallMediaState): void {
    if (this.state === state) return;
    this.state = state;
    this.events.onStateChange?.(state);
  }
}

function expectedVoiceAgentUserId(memberUserId: string): string {
  const normalized = memberUserId.trim();
  const memberPrefix = "voice-member-";
  if (
    !normalized.startsWith(memberPrefix) ||
    normalized.length === memberPrefix.length
  ) {
    throw new VoiceCallMediaError(
      "join_failed",
      "Voice call media has an invalid member identity",
    );
  }
  return `voice-agent-${normalized.slice(memberPrefix.length)}`;
}

export function createVolcengineVoiceMediaSession(
  events?: VoiceCallMediaEvents,
): VolcengineVoiceMediaSession {
  return new VolcengineVoiceMediaSession(events);
}
