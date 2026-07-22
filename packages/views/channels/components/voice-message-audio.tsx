"use client";

import { useCallback, useEffect, useRef, useState } from "react";
import { LoaderCircle, Mic, Play, RotateCcw, Square, Volume2 } from "lucide-react";
import type { ChannelMessage } from "@multica/core/types";
import { cn } from "@multica/ui/lib/utils";
import { useT } from "../../i18n/use-t";
import {
  claimVoiceAutoplay,
  startVoicePlayback,
  type VoicePlayback,
  voicePlaybackScope,
} from "../lib/voice-playback";

type PlaybackState = "idle" | "loading" | "playing" | "error";

export function VoiceMessageAudio({ message }: { message: ChannelMessage }) {
  const { t } = useT("channels");
  const voicePart = message.parts?.find((part) => part.type === "voice");
  const hasVoicePart = Boolean(voicePart);
  const [state, setState] = useState<PlaybackState>("idle");
  const playbackRef = useRef<VoicePlayback | null>(null);
  const mountedRef = useRef(true);
  const startRef = useRef<(() => Promise<void>) | null>(null);

  const start = useCallback(async () => {
    if (!voicePart || !message.content.trim() || state === "loading" || state === "playing") return;
    setState("loading");
    try {
      const playback = await startVoicePlayback(message.content);
      if (!mountedRef.current) {
        playback.stop();
        return;
      }
      playbackRef.current = playback;
      setState("playing");
      await playback.finished;
      playbackRef.current = null;
      if (mountedRef.current) setState("idle");
    } catch {
      playbackRef.current = null;
      if (mountedRef.current) setState("error");
    }
  }, [message.content, state, voicePart]);
  startRef.current = start;

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
        "mt-1.5 inline-flex min-h-8 items-center gap-1.5 rounded-full border border-primary/20 bg-primary/[0.06] px-3 text-xs font-medium text-primary transition-colors hover:bg-primary/10 focus-visible:outline-none focus-visible:ring-1 focus-visible:ring-ring",
        state === "error" && "border-destructive/30 bg-destructive/5 text-destructive hover:bg-destructive/10",
      )}
      onClick={state === "playing" ? stop : () => void start()}
      disabled={state === "loading"}
      aria-label={label}
      data-testid="voice-reply-control"
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
      {state === "playing" && <Volume2 className="size-3.5" />}
      <span>{label}</span>
    </button>
  );
}
