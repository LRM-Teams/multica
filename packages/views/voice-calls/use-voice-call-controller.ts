import {
  useCreateVoiceCall,
  useConnectVoiceCall,
  useStopVoiceCall,
  voiceCallOptions,
} from "@multica/core/voice-calls";
import type {
  CreateVoiceCallRequest,
  VoiceCall,
  VoiceCallMedia,
} from "@multica/core/types";
import { useQuery, useQueryClient } from "@tanstack/react-query";
import {
  useCallback,
  useEffect,
  useRef,
  useState,
} from "react";
import {
  createVolcengineVoiceMediaSession,
  VoiceCallMediaError,
  type VoiceCallMediaEvents,
  type VoiceCallMediaState,
} from "./volcengine-media-session";
import {
  createVoiceCallRingback,
  type VoiceCallRingback,
  type VoiceCallRingbackFactory,
} from "./voice-call-ringback";

export type VoiceCallControllerPhase =
  | "idle"
  | "creating"
  | "joining"
  | "connected"
  | "muted"
  | "reconnecting"
  | "ending"
  | "ended"
  | "failed";

export type VoiceCallControllerErrorSource =
  | "create"
  | "media"
  | "stop"
  | "server";

export interface VoiceCallControllerError {
  source: VoiceCallControllerErrorSource;
  code: string;
  message: string;
  providerCode?: string;
}

export interface VoiceCallMediaSession {
  connect(media: VoiceCallMedia, deviceId?: string): Promise<void>;
  setMuted(muted: boolean): Promise<void>;
  resumeRemoteAudio(remoteUserId: string): Promise<void>;
  disconnect(): Promise<void>;
}

export type VoiceCallMediaSessionFactory = (
  events: VoiceCallMediaEvents,
) => VoiceCallMediaSession;

export interface UseVoiceCallControllerOptions {
  mediaSessionFactory?: VoiceCallMediaSessionFactory;
  ringbackFactory?: VoiceCallRingbackFactory;
  activationTimeoutMs?: number;
}

export interface VoiceCallController {
  call: VoiceCall | null;
  callId: string;
  phase: VoiceCallControllerPhase;
  error: VoiceCallControllerError | null;
  autoplayBlockedUserId: string | null;
  start(
    input: CreateVoiceCallRequest,
    microphoneDeviceId?: string,
  ): Promise<string>;
  hangUp(): Promise<void>;
  setMuted(muted: boolean): Promise<void>;
  resumeRemoteAudio(): Promise<void>;
}

const TERMINAL_STATUSES = new Set(["ended", "failed"]);
const DEFAULT_ACTIVATION_TIMEOUT_MS = 30_000;

function errorMessage(error: unknown, fallback: string): string {
  return error instanceof Error && error.message.trim()
    ? error.message
    : fallback;
}

function controllerError(
  source: VoiceCallControllerErrorSource,
  code: string,
  error: unknown,
  fallback: string,
): VoiceCallControllerError {
  return {
    source,
    code: error instanceof VoiceCallMediaError ? error.code : code,
    message: errorMessage(error, fallback),
    providerCode: error instanceof VoiceCallMediaError
      ? error.providerCode
      : undefined,
  };
}

function phaseFromMediaState(
  state: VoiceCallMediaState,
): VoiceCallControllerPhase | null {
  switch (state) {
    case "joining":
      return "joining";
    case "connected":
      return "connected";
    case "muted":
      return "muted";
    case "reconnecting":
      return "reconnecting";
    case "failed":
      return "failed";
    case "idle":
    case "closed":
      return null;
  }
}

function phaseFromServer(
  status: string | undefined,
  localPhase: VoiceCallControllerPhase,
): VoiceCallControllerPhase {
  if (localPhase === "failed" && status !== "failed") {
    return "failed";
  }
  if (localPhase === "ended" && status !== "failed") {
    return "ended";
  }
  switch (status) {
    case "starting":
    case "connecting":
      return localPhase === "connected" ||
          localPhase === "muted" ||
          localPhase === "reconnecting" ||
          localPhase === "ending"
        ? localPhase
        : "joining";
    case "active":
      return localPhase === "muted" ||
          localPhase === "reconnecting" ||
          localPhase === "ending"
        ? localPhase
        : "connected";
    case "reconnecting":
      return localPhase === "ending" ? "ending" : "reconnecting";
    case "ending":
      return "ending";
    case "ended":
      return "ended";
    case "failed":
      return "failed";
    default:
      return localPhase;
  }
}

export function useVoiceCallController(
  workspaceId: string,
  options: UseVoiceCallControllerOptions = {},
): VoiceCallController {
  const mediaSessionFactory =
    options.mediaSessionFactory ?? createVolcengineVoiceMediaSession;
  const ringbackFactory =
    options.ringbackFactory ?? createVoiceCallRingback;
  const activationTimeoutMs =
    options.activationTimeoutMs ?? DEFAULT_ACTIVATION_TIMEOUT_MS;
  const queryClient = useQueryClient();
  const createCallMutation = useCreateVoiceCall(workspaceId);
  const connectCallMutation = useConnectVoiceCall(workspaceId);
  const stopCallMutation = useStopVoiceCall(workspaceId);
  const [callId, setCallId] = useState("");
  const [localPhase, setLocalPhase] =
    useState<VoiceCallControllerPhase>("idle");
  const [localError, setLocalError] =
    useState<VoiceCallControllerError | null>(null);
  const [autoplayBlockedUserId, setAutoplayBlockedUserId] =
    useState<string | null>(null);
  const callQuery = useQuery(voiceCallOptions(workspaceId, callId));

  const mountedRef = useRef(true);
  const activeCallIdRef = useRef("");
  const mediaSessionRef = useRef<VoiceCallMediaSession | null>(null);
  const ringbackRef = useRef<VoiceCallRingback | null>(null);
  const startPromiseRef = useRef<Promise<string> | null>(null);
  const cancelRequestedRef = useRef(false);
  const endingRef = useRef(false);
  const mediaFailureRef = useRef<unknown>(null);
  const providerStartedRef = useRef(false);
  const providerAnsweredRef = useRef(false);
  const activationTimeoutRef =
    useRef<ReturnType<typeof globalThis.setTimeout> | null>(null);
  const stoppedCallIdsRef = useRef(new Set<string>());
  const stopInFlightRef = useRef<{
    callId: string;
    promise: Promise<void>;
  } | null>(null);

  const requestServerStop = useCallback((targetCallId: string) => {
    if (stoppedCallIdsRef.current.has(targetCallId)) {
      return Promise.resolve();
    }
    if (stopInFlightRef.current?.callId === targetCallId) {
      return stopInFlightRef.current.promise;
    }

    const promise = stopCallMutation.mutateAsync(targetCallId)
      .then(() => {
        stoppedCallIdsRef.current.add(targetCallId);
      })
      .finally(() => {
        if (stopInFlightRef.current?.callId === targetCallId) {
          stopInFlightRef.current = null;
        }
      });
    stopInFlightRef.current = { callId: targetCallId, promise };
    return promise;
  }, [stopCallMutation]);
  const requestServerStopRef = useRef(requestServerStop);
  requestServerStopRef.current = requestServerStop;

  const stopRingback = useCallback(() => {
    ringbackRef.current?.stop();
    ringbackRef.current = null;
  }, []);

  const startRingback = useCallback(() => {
    stopRingback();
    let ringback: VoiceCallRingback | null = null;
    try {
      ringback = ringbackFactory();
      ringbackRef.current = ringback;
      ringback.start();
    } catch {
      // Ringback is local call-progress feedback. A browser audio failure must
      // not prevent the RTC call itself from connecting.
      ringback?.stop();
      ringbackRef.current = null;
    }
  }, [ringbackFactory, stopRingback]);

  const clearActivationTimeout = useCallback(() => {
    if (activationTimeoutRef.current === null) return;
    globalThis.clearTimeout(activationTimeoutRef.current);
    activationTimeoutRef.current = null;
  }, []);

  const scheduleActivationTimeout = useCallback((targetCallId: string) => {
    clearActivationTimeout();
    if (providerAnsweredRef.current) return;
    activationTimeoutRef.current = globalThis.setTimeout(() => {
      activationTimeoutRef.current = null;
      void (async () => {
        if (
          activeCallIdRef.current !== targetCallId ||
          endingRef.current ||
          cancelRequestedRef.current ||
          !providerStartedRef.current ||
          providerAnsweredRef.current
        ) {
          return;
        }

        let latestStatus = "";
        try {
          const latest = await queryClient.fetchQuery({
            ...voiceCallOptions(workspaceId, targetCallId),
            staleTime: 0,
          });
          latestStatus = latest.call.status;
        } catch (error) {
          if (
            activeCallIdRef.current !== targetCallId ||
            endingRef.current ||
            cancelRequestedRef.current
          ) {
            return;
          }
          stopRingback();
          providerStartedRef.current = false;
          if (mountedRef.current) {
            setLocalError(controllerError(
              "server",
              "provider_status_check_failed",
              error,
              "Could not confirm whether the voice call agent answered",
            ));
            setLocalPhase("failed");
          }
          const session = mediaSessionRef.current;
          mediaSessionRef.current = null;
          const [, stopResult] = await Promise.allSettled([
            session?.disconnect() ?? Promise.resolve(),
            requestServerStopRef.current(targetCallId),
          ]);
          if (stopResult.status === "fulfilled") {
            if (activeCallIdRef.current === targetCallId) {
              activeCallIdRef.current = "";
            }
          } else if (mountedRef.current) {
            setLocalError(controllerError(
              "stop",
              "stop_failed",
              stopResult.reason,
              "The unconfirmed voice call could not be stopped on the server",
            ));
          }
          return;
        }

        if (
          activeCallIdRef.current !== targetCallId ||
          endingRef.current ||
          cancelRequestedRef.current ||
          providerAnsweredRef.current
        ) {
          return;
        }
        if (latestStatus === "active") {
          stopRingback();
          if (mountedRef.current) {
            setLocalPhase("connected");
          }
          return;
        }
        if (TERMINAL_STATUSES.has(latestStatus)) {
          stopRingback();
          return;
        }

        stopRingback();
        providerStartedRef.current = false;
        if (mountedRef.current) {
          setLocalError({
            source: "server",
            code: "provider_activation_timeout",
            message: "The voice call agent did not answer in time",
          });
          setLocalPhase("failed");
        }
        const session = mediaSessionRef.current;
        mediaSessionRef.current = null;
        const [, stopResult] = await Promise.allSettled([
          session?.disconnect() ?? Promise.resolve(),
          requestServerStopRef.current(targetCallId),
        ]);
        if (stopResult.status === "fulfilled") {
          if (activeCallIdRef.current === targetCallId) {
            activeCallIdRef.current = "";
          }
          return;
        }
        if (mountedRef.current) {
          setLocalError(controllerError(
            "stop",
            "stop_failed",
            stopResult.reason,
            "The unanswered voice call could not be stopped on the server",
          ));
        }
      })();
    }, activationTimeoutMs);
  }, [
    activationTimeoutMs,
    clearActivationTimeout,
    queryClient,
    stopRingback,
    workspaceId,
  ]);

  const setMediaFailure = useCallback((error: unknown) => {
    if (endingRef.current || cancelRequestedRef.current) return;
    clearActivationTimeout();
    stopRingback();
    mediaFailureRef.current = error;
    if (mountedRef.current) {
      setLocalError(controllerError(
        "media",
        "media_failed",
        error,
        "Voice call media failed",
      ));
      setLocalPhase("failed");
    }
    const targetCallId = activeCallIdRef.current;
    if (targetCallId && !startPromiseRef.current) {
      void requestServerStopRef.current(targetCallId).catch(
        (stopError: unknown) => {
          if (mountedRef.current) {
            setLocalError(controllerError(
              "stop",
              "stop_failed",
              stopError,
              "Voice call media failed and the server call could not be stopped",
            ));
          }
        },
      );
    }
  }, [clearActivationTimeout, stopRingback]);

  const start = useCallback((
    input: CreateVoiceCallRequest,
    microphoneDeviceId?: string,
  ): Promise<string> => {
    if (startPromiseRef.current || activeCallIdRef.current) {
      return Promise.reject(
        new VoiceCallMediaError(
          "already_started",
          "A voice call is already in progress",
        ),
      );
    }

    cancelRequestedRef.current = false;
    endingRef.current = false;
    mediaFailureRef.current = null;
    providerStartedRef.current = false;
    providerAnsweredRef.current = false;
    clearActivationTimeout();
    setCallId("");
    setAutoplayBlockedUserId(null);
    setLocalError(null);
    setLocalPhase("creating");
    startRingback();

    const operation = (async () => {
      let createdCallId = "";
      let cleanupAttempted = false;
      let failureSource: VoiceCallControllerErrorSource = "create";
      try {
        const created = await createCallMutation.mutateAsync(input);
        failureSource = "media";
        createdCallId = created.call.id;
        activeCallIdRef.current = createdCallId;
        if (mountedRef.current) {
          setCallId(createdCallId);
        }

        if (cancelRequestedRef.current) {
          cleanupAttempted = true;
          await requestServerStopRef.current(createdCallId).catch(
            (stopError: unknown) => {
              if (mountedRef.current) {
                setLocalError(controllerError(
                  "stop",
                  "stop_failed",
                  stopError,
                  "Cancelled voice call could not be stopped on the server",
                ));
              }
              throw stopError;
            },
          );
          activeCallIdRef.current = "";
          if (mountedRef.current) setLocalPhase("ended");
          throw new VoiceCallMediaError(
            "cancelled",
            "Voice call startup was cancelled",
          );
        }

        const events: VoiceCallMediaEvents = {
          onStateChange: (state) => {
            if (
              !providerStartedRef.current &&
              !providerAnsweredRef.current &&
              (state === "connected" || state === "muted")
            ) {
              return;
            }
            if (
              state === "connected" ||
              state === "muted" ||
              state === "failed" ||
              state === "closed"
            ) {
              stopRingback();
            }
            const nextPhase = phaseFromMediaState(state);
            if (nextPhase && mountedRef.current) {
              setLocalPhase(nextPhase);
            }
          },
          onAutoplayBlocked: (remoteUserId) => {
            if (mountedRef.current) {
              setAutoplayBlockedUserId(remoteUserId);
            }
          },
          onRemoteAudioStarted: () => {
            if (endingRef.current || cancelRequestedRef.current) return;
            providerAnsweredRef.current = true;
            clearActivationTimeout();
            stopRingback();
            if (mountedRef.current) {
              setLocalPhase((current) =>
                current === "muted" ? "muted" : "connected"
              );
            }
          },
          onError: setMediaFailure,
        };
        const session = mediaSessionFactory(events);
        mediaSessionRef.current = session;
        await session.connect(created.media, microphoneDeviceId);
        if (mediaFailureRef.current) {
          throw mediaFailureRef.current;
        }

        if (cancelRequestedRef.current) {
          await session.disconnect().catch(() => undefined);
          cleanupAttempted = true;
          await requestServerStopRef.current(createdCallId).catch(
            (stopError: unknown) => {
              if (mountedRef.current) {
                setLocalError(controllerError(
                  "stop",
                  "stop_failed",
                  stopError,
                  "Cancelled voice call could not be stopped on the server",
                ));
              }
              throw stopError;
            },
          );
          activeCallIdRef.current = "";
          if (mountedRef.current) setLocalPhase("ended");
          throw new VoiceCallMediaError(
            "cancelled",
            "Voice call startup was cancelled",
          );
        }
        failureSource = "server";
        await connectCallMutation.mutateAsync(createdCallId);
        providerStartedRef.current = true;
        if (providerAnsweredRef.current) {
          stopRingback();
          if (mountedRef.current) {
            setLocalPhase((current) =>
              current === "muted" ? "muted" : "connected"
            );
          }
        } else {
          scheduleActivationTimeout(createdCallId);
        }
        return createdCallId;
      } catch (error) {
        clearActivationTimeout();
        stopRingback();
        const cancelled =
          error instanceof VoiceCallMediaError && error.code === "cancelled";
        if (createdCallId && !cancelled && !cleanupAttempted) {
          await mediaSessionRef.current?.disconnect().catch(() => undefined);
          mediaSessionRef.current = null;
          let stopFailed = false;
          await requestServerStopRef.current(createdCallId).catch(
            (stopError: unknown) => {
              stopFailed = true;
              if (mountedRef.current) {
                setLocalError(controllerError(
                  "stop",
                  "stop_failed",
                  stopError,
                  "Voice call media failed and the server call could not be stopped",
                ));
              }
            },
          );
          if (!stopFailed) {
            activeCallIdRef.current = "";
          }
        }
        if (!cancelled && mountedRef.current) {
          const source = failureSource;
          setLocalError((current) => current ?? controllerError(
            source,
            source === "create"
              ? "create_failed"
              : source === "server"
                ? "provider_start_failed"
                : "media_failed",
            error,
            source === "create"
              ? "Failed to create voice call"
              : source === "server"
                ? "Failed to start the voice call agent"
                : "Failed to start voice call media",
          ));
          setLocalPhase("failed");
        }
        throw error;
      }
    })();

    startPromiseRef.current = operation;
    void operation.then(
      () => {
        if (startPromiseRef.current === operation) {
          startPromiseRef.current = null;
        }
      },
      () => {
        if (startPromiseRef.current === operation) {
          startPromiseRef.current = null;
        }
      },
    );
    return operation;
  }, [
    createCallMutation,
    connectCallMutation,
    clearActivationTimeout,
    mediaSessionFactory,
    scheduleActivationTimeout,
    setMediaFailure,
    startRingback,
    stopRingback,
  ]);

  const hangUp = useCallback(async (): Promise<void> => {
    clearActivationTimeout();
    stopRingback();
    cancelRequestedRef.current = true;
    endingRef.current = true;
    if (mountedRef.current) setLocalPhase("ending");

    const targetCallId = activeCallIdRef.current;
    if (!targetCallId) {
      try {
        await startPromiseRef.current;
      } catch (error) {
        if (activeCallIdRef.current) {
          endingRef.current = false;
          throw error;
        }
      }
      if (mountedRef.current && !activeCallIdRef.current) {
        setLocalPhase("ended");
      }
      endingRef.current = false;
      return;
    }

    const [mediaResult, stopResult] = await Promise.allSettled([
      mediaSessionRef.current?.disconnect() ?? Promise.resolve(),
      requestServerStopRef.current(targetCallId),
    ]);
    if (stopResult.status === "rejected") {
      endingRef.current = false;
      if (mountedRef.current) {
        setLocalError(controllerError(
          "stop",
          "stop_failed",
          stopResult.reason,
          "Failed to stop voice call",
        ));
        setLocalPhase("failed");
      }
      throw stopResult.reason;
    }

    activeCallIdRef.current = "";
    mediaSessionRef.current = null;
    endingRef.current = false;
    if (mountedRef.current) {
      if (mediaResult.status === "rejected") {
        setLocalError(controllerError(
          "media",
          "cleanup_failed",
          mediaResult.reason,
          "Voice call media cleanup was incomplete",
        ));
      }
      setLocalPhase("ended");
    }
  }, [clearActivationTimeout, stopRingback]);

  const setMuted = useCallback(async (muted: boolean): Promise<void> => {
    const session = mediaSessionRef.current;
    if (!session) {
      throw new VoiceCallMediaError(
        muted ? "mute_failed" : "unmute_failed",
        "Voice call media is not connected",
      );
    }
    try {
      await session.setMuted(muted);
    } catch (error) {
      if (mountedRef.current) {
        setLocalError(controllerError(
          "media",
          muted ? "mute_failed" : "unmute_failed",
          error,
          muted
            ? "Failed to mute microphone"
            : "Failed to resume microphone",
        ));
      }
      throw error;
    }
  }, []);

  const resumeRemoteAudio = useCallback(async (): Promise<void> => {
    const remoteUserId = autoplayBlockedUserId;
    const session = mediaSessionRef.current;
    if (!session || !remoteUserId) {
      throw new VoiceCallMediaError(
        "playback_failed",
        "Remote voice playback is not blocked",
      );
    }
    try {
      await session.resumeRemoteAudio(remoteUserId);
      if (mountedRef.current) setAutoplayBlockedUserId(null);
    } catch (error) {
      if (mountedRef.current) {
        setLocalError(controllerError(
          "media",
          "playback_failed",
          error,
          "Failed to resume remote voice playback",
        ));
      }
      throw error;
    }
  }, [autoplayBlockedUserId]);

  const serverCall = callQuery.data?.call ?? null;
  const serverStatus = serverCall?.status;

  useEffect(() => {
    if (!callId) return;
    if (serverStatus === "active") {
      clearActivationTimeout();
      stopRingback();
      return;
    }
    if (!TERMINAL_STATUSES.has(serverStatus ?? "")) return;
    clearActivationTimeout();
    stopRingback();
    if (activeCallIdRef.current === callId) {
      activeCallIdRef.current = "";
    }
    const session = mediaSessionRef.current;
    mediaSessionRef.current = null;
    void session?.disconnect().catch(() => undefined);
  }, [callId, clearActivationTimeout, serverStatus, stopRingback]);

  useEffect(() => {
    mountedRef.current = true;
    return () => {
      mountedRef.current = false;
      cancelRequestedRef.current = true;
      endingRef.current = true;
      clearActivationTimeout();
      stopRingback();
      const targetCallId = activeCallIdRef.current;
      activeCallIdRef.current = "";
      void mediaSessionRef.current?.disconnect().catch(() => undefined);
      mediaSessionRef.current = null;
      if (targetCallId) {
        void requestServerStopRef.current(targetCallId).catch(() => undefined);
      }
    };
  }, [clearActivationTimeout, stopRingback]);

  const serverFailure = serverStatus === "failed" && serverCall
    ? {
      source: "server" as const,
      code: serverCall.error_code || "server_failed",
      message: "Voice call ended because the server reported a failure",
    }
    : null;

  return {
    call: serverCall,
    callId,
    phase: phaseFromServer(serverStatus, localPhase),
    error: localError ?? serverFailure,
    autoplayBlockedUserId,
    start,
    hangUp,
    setMuted,
    resumeRemoteAudio,
  };
}
