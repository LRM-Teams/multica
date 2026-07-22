"use client";

import { useCallback, useEffect, useRef, useState } from "react";
import { AudioLines, LoaderCircle, Mic, Play, RotateCcw, Square } from "lucide-react";
import type { ChannelMessage } from "@multica/core/types";
import { cn } from "@multica/ui/lib/utils";
import { useT } from "../../i18n/use-t";
import {
  claimVoiceAutoplay,
  prepareVoiceAudio,
  startPreparedVoicePlayback,
  type VoicePlayback,
  voicePlaybackScope,
} from "../lib/voice-playback";
import {
  resolveVoiceMessagePresentation,
  voiceBubbleWidthPx,
  type VoiceMessagePresentation,
} from "../lib/voice-message-presentation";

type PlaybackState = "idle" | "loading" | "playing" | "error";

export function VoiceMessageAudio({
  message,
  presentation,
}: {
  message: ChannelMessage;
  presentation?: VoiceMessagePresentation | null;
}) {
  const { t } = useT("channels");
  const resolvedPresentation = presentation === undefined
    ? resolveVoiceMessagePresentation(message)
    : presentation;
  const voicePart = resolvedPresentation?.voicePart;
  const hasVoicePart = Boolean(voicePart);
  const [state, setState] = useState<PlaybackState>("idle");
  const [durationSeconds, setDurationSeconds] = useState<number | null>(() =>
    voicePart?.duration_ms ? Math.max(1, Math.round(voicePart.duration_ms / 1000)) : null,
  );
  const playbackRef = useRef<VoicePlayback | null>(null);
  const mountedRef = useRef(true);
  const startRef = useRef<(() => Promise<void>) | null>(null);

  const start = useCallback(async () => {
    if (!hasVoicePart || !message.content.trim() || state === "loading" || state === "playing") return;
    setState("loading");
    try {
      const prepared = await prepareVoiceAudio(message.content);
      if (mountedRef.current) {
        if (prepared.durationMs) {
          setDurationSeconds(Math.max(1, Math.round(prepared.durationMs / 1000)));
        }
        const playback = await startPreparedVoicePlayback(prepared);
        if (mountedRef.current) {
          playbackRef.current = playback;
          setDurationSeconds(Math.max(1, Math.round(playback.durationMs / 1000)));
          setState("playing");
          await playback.finished;
          playbackRef.current = null;
          if (mountedRef.current) setState("idle");
        } else {
          playback.stop();
        }
      }
    } catch {
      playbackRef.current = null;
      if (mountedRef.current) setState("error");
    }
  }, [hasVoicePart, message.content, state]);
  startRef.current = start;

  useEffect(() => {
    if (!hasVoicePart || message.type !== "agent" || !message.content.trim()) return;
    let current = true;
    void prepareVoiceAudio(message.content)
      .then((prepared) => {
        if (current && prepared.durationMs) {
          setDurationSeconds(Math.max(1, Math.round(prepared.durationMs / 1000)));
        }
      })
      .catch(() => {
        if (current) setState("error");
      });
    return () => {
      current = false;
    };
  }, [hasVoicePart, message.content, message.type]);

  useEffect(() => {
    mountedRef.current = true;
    if (
      message.type === "agent" &&
      claimVoiceAutoplay(
        message.id,
        voicePlaybackScope(message.channel_id, message.thread_root_message_id),
        message.created_at,
      )
    ) {
      if (hasVoicePart) void startRef.current?.();
    }
    return () => {
      mountedRef.current = false;
      playbackRef.current?.stop();
      playbackRef.current = null;
    };
  }, [hasVoicePart, message.channel_id, message.created_at, message.id, message.thread_root_message_id, message.type]);

  if (!voicePart) return null;
  if (message.type !== "agent") {
    const seconds = voicePart.duration_ms ? Math.max(1, Math.round(voicePart.duration_ms / 1000)) : null;
    return (
      <div className="mt-1 inline-flex items-center gap-1.5 text-xs text-muted-foreground" data-testid="voice-input-label">
        <Mic className="size-3.5" />
        <span>
          {seconds
            ? t(($) => $.message.voice_input_duration, { seconds })
            : t(($) => $.message.voice_input)}
        </span>
      </div>
    );
  }

  const stop = () => playbackRef.current?.stop();
  const label = state === "loading"
    ? t(($) => $.message.voice_loading)
    : state === "playing"
      ? t(($) => $.message.voice_stop)
      : state === "error"
        ? t(($) => $.message.voice_retry)
        : t(($) => $.message.voice_play);

  return (
    <button
      type="button"
      className={cn(
        "mt-1.5 inline-flex min-h-10 min-w-28 items-center justify-between gap-2 rounded-2xl rounded-bl-sm border border-primary/20 bg-primary/[0.08] px-3 py-2 text-xs font-medium text-primary shadow-sm transition-colors hover:bg-primary/[0.12] focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring",
        state === "error" && "border-destructive/30 bg-destructive/5 text-destructive hover:bg-destructive/10",
      )}
      onClick={state === "playing" ? stop : () => void start()}
      disabled={state === "loading"}
      aria-label={label}
      data-testid="voice-reply-control"
      data-voice-bubble="true"
      style={{ width: voiceBubbleWidthPx(durationSeconds) }}
    >
      {state === "loading" ? (
        <LoaderCircle className="size-3.5 animate-spin" />
      ) : state === "playing" ? (
        <Square className="size-3 fill-current" />
      ) : state === "error" ? (
        <RotateCcw className="size-3.5" />
      ) : (
        <Play className="size-3.5 fill-current" />
      )}
      <AudioLines
        className={cn("size-5", state === "playing" && "animate-pulse")}
        aria-hidden="true"
      />
      <span className="min-w-5 text-right tabular-nums" aria-hidden="true">
        {durationSeconds ? `${durationSeconds}″` : "…"}
      </span>
    </button>
  );
}
