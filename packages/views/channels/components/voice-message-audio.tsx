"use client";

import { useCallback, useEffect, useRef, useState } from "react";
import {
  AudioLines,
  Captions,
  CaptionsOff,
  LoaderCircle,
  Mic,
  Play,
  RotateCcw,
  Square,
  VolumeX,
} from "lucide-react";
import type { ChannelMessage } from "@multica/core/types";
import { resolvePublicFileUrl } from "@multica/core/workspace/avatar-url";
import { cn } from "@multica/ui/lib/utils";
import { useT } from "../../i18n/use-t";
import { MessageBody } from "./message-body";
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
  highlightQuery,
}: {
  message: ChannelMessage;
  presentation?: VoiceMessagePresentation | null;
  highlightQuery?: string;
}) {
  const { t } = useT("channels");
  const resolvedPresentation = presentation === undefined
    ? resolveVoiceMessagePresentation(message)
    : presentation;
  const voicePart = resolvedPresentation?.voicePart;
  const transcriptionStatus = voicePart?.transcription_status;
  const synthesisStatus = voicePart?.synthesis_status;
  const transcriptAvailable =
    Boolean(message.content.trim()) &&
    transcriptionStatus !== "pending" &&
    transcriptionStatus !== "failed";
  const hasVoicePart = Boolean(voicePart);
  const recordingAttachment = resolvedPresentation?.source === "recording"
    ? resolvedPresentation.recordingAttachment
    : null;
  const recordingURL = recordingAttachment
    ? resolvePublicFileUrl(recordingAttachment.download_url) ?? recordingAttachment.download_url
    : null;
  const serverSynthesisOwned =
    message.type === "agent" && synthesisStatus !== undefined;
  const synthesisPending =
    serverSynthesisOwned &&
    synthesisStatus === "pending" &&
    !recordingAttachment;
  const synthesisUnavailable =
    serverSynthesisOwned &&
    synthesisStatus !== "pending" &&
    !recordingAttachment;
  const synthesisBlocked = synthesisPending || synthesisUnavailable;
  const [state, setState] = useState<PlaybackState>("idle");
  const [durationSeconds, setDurationSeconds] = useState<number | null>(() =>
    voicePart?.duration_ms ? Math.max(1, Math.round(voicePart.duration_ms / 1000)) : null,
  );
  const [transcriptExpanded, setTranscriptExpanded] = useState(false);
  const playbackRef = useRef<VoicePlayback | null>(null);
  const recordingAudioRef = useRef<HTMLAudioElement | null>(null);
  const mountedRef = useRef(true);
  const startRef = useRef<(() => Promise<void>) | null>(null);

  const start = useCallback(async () => {
    if (
      !hasVoicePart ||
      synthesisBlocked ||
      (!recordingAttachment && !message.content.trim()) ||
      state === "loading" ||
      state === "playing"
    ) return;
    setState("loading");
    try {
      if (recordingAttachment) {
        if (!recordingURL) throw new Error("voice recording URL is unavailable");
        let audio = recordingAudioRef.current;
        if (!audio) {
          const created = new Audio(recordingURL);
          created.preload = "metadata";
          created.onended = () => {
            if (mountedRef.current) setState("idle");
          };
          created.onerror = () => {
            if (mountedRef.current) setState("error");
          };
          created.ondurationchange = () => {
            if (mountedRef.current && Number.isFinite(created.duration) && created.duration > 0) {
              setDurationSeconds(Math.max(1, Math.round(created.duration)));
            }
          };
          recordingAudioRef.current = created;
          audio = created;
        }
        await audio.play();
        if (mountedRef.current) setState("playing");
        return;
      }
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
  }, [
    hasVoicePart,
    message.content,
    recordingAttachment,
    recordingURL,
    state,
    synthesisBlocked,
  ]);
  startRef.current = start;

  useEffect(() => {
    if (
      !hasVoicePart ||
      recordingAttachment ||
      serverSynthesisOwned ||
      message.type !== "agent" ||
      !message.content.trim()
    ) return;
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
  }, [
    hasVoicePart,
    message.content,
    message.type,
    recordingAttachment,
    serverSynthesisOwned,
  ]);

  useEffect(() => {
    mountedRef.current = true;
    if (
      message.type === "agent" &&
      !synthesisBlocked &&
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
      const recording = recordingAudioRef.current;
      recordingAudioRef.current = null;
      if (recording) {
        recording.pause();
        recording.onended = null;
        recording.onerror = null;
        recording.ondurationchange = null;
      }
    };
  }, [
    hasVoicePart,
    message.channel_id,
    message.created_at,
    message.id,
    message.thread_root_message_id,
    message.type,
    synthesisBlocked,
  ]);

  if (!voicePart) return null;
  if (message.type !== "agent" && !recordingAttachment) {
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

  const stop = () => {
    if (recordingAudioRef.current) {
      recordingAudioRef.current.pause();
      setState("idle");
      return;
    }
    playbackRef.current?.stop();
  };
  const label = synthesisPending
    ? t(($) => $.message.voice_synthesizing)
    : synthesisUnavailable
      ? t(($) => $.message.voice_synthesis_unavailable)
      : state === "loading"
        ? t(($) => $.message.voice_loading)
        : state === "playing"
          ? t(($) => $.message.voice_stop)
          : state === "error"
            ? t(($) => $.message.voice_retry)
            : t(($) => $.message.voice_play);
  const transcriptToggleLabel = transcriptExpanded
    ? t(($) => $.message.voice_hide_transcript)
    : t(($) => $.message.voice_show_transcript);
  const transcriptPanelId = `voice-transcript-${message.id}`;
  const transcriptLabelId = `voice-transcript-label-${message.id}`;

  return (
    <div className="mt-1.5 flex max-w-full flex-col items-start" data-testid="voice-reply">
      <div className="flex max-w-full flex-wrap items-center gap-1.5">
        <button
          type="button"
          className={cn(
            "inline-flex min-h-10 min-w-28 items-center justify-between gap-2 rounded-2xl rounded-bl-sm border border-primary/20 bg-primary/[0.08] px-3 py-2 text-xs font-medium text-primary shadow-sm transition-colors hover:bg-primary/[0.12] focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring",
            state === "error" && "border-destructive/30 bg-destructive/5 text-destructive hover:bg-destructive/10",
          )}
          onClick={state === "playing" ? stop : () => void start()}
          disabled={state === "loading" || synthesisBlocked}
          aria-label={label}
          aria-live={synthesisBlocked ? undefined : "polite"}
          aria-busy={state === "loading" || synthesisPending}
          data-testid="voice-reply-control"
          data-voice-bubble="true"
          style={{ width: voiceBubbleWidthPx(durationSeconds) }}
        >
          {state === "loading" || synthesisPending ? (
            <LoaderCircle
              className="size-3.5 animate-spin motion-reduce:animate-none"
              aria-hidden="true"
            />
          ) : synthesisUnavailable ? (
            <VolumeX className="size-3.5" aria-hidden="true" />
          ) : state === "playing" ? (
            <Square className="size-3 fill-current" aria-hidden="true" />
          ) : state === "error" ? (
            <RotateCcw className="size-3.5" aria-hidden="true" />
          ) : (
            <Play className="size-3.5 fill-current" aria-hidden="true" />
          )}
          <AudioLines
            className={cn(
              "size-5",
              state === "playing" && "animate-pulse motion-reduce:animate-none",
            )}
            aria-hidden="true"
          />
          <span className="min-w-5 text-right tabular-nums" aria-hidden="true">
            {durationSeconds ? `${durationSeconds}″` : "…"}
          </span>
        </button>
        {synthesisPending ? (
          <output
            className="inline-flex min-h-9 shrink-0 items-center gap-1.5 px-2 text-[11px] font-medium text-muted-foreground"
            aria-live="polite"
          >
            <LoaderCircle
              className="size-3.5 animate-spin motion-reduce:animate-none"
              aria-hidden="true"
            />
            {t(($) => $.message.voice_synthesizing)}
          </output>
        ) : synthesisUnavailable ? (
          <output
            className="inline-flex min-h-9 shrink-0 items-center gap-1.5 px-2 text-[11px] font-medium text-muted-foreground"
            aria-live="polite"
          >
            <VolumeX className="size-3.5" aria-hidden="true" />
            {t(($) => $.message.voice_synthesis_unavailable)}
          </output>
        ) : null}
        {transcriptionStatus === "pending" ? (
          <output
            className="inline-flex min-h-9 shrink-0 items-center gap-1.5 px-2 text-[11px] font-medium text-muted-foreground"
            aria-live="polite"
          >
            <LoaderCircle
              className="size-3.5 animate-spin motion-reduce:animate-none"
              aria-hidden="true"
            />
            {t(($) => $.message.voice_transcribing)}
          </output>
        ) : transcriptionStatus === "failed" ? (
          <output
            className="inline-flex min-h-9 shrink-0 items-center gap-1.5 px-2 text-[11px] font-medium text-muted-foreground"
            aria-live="polite"
          >
            <CaptionsOff className="size-3.5" aria-hidden="true" />
            {t(($) => $.message.voice_transcription_unavailable)}
          </output>
        ) : transcriptAvailable ? (
          <button
            type="button"
            className={cn(
              "inline-flex min-h-9 shrink-0 items-center gap-1 rounded-lg px-2.5 text-[11px] font-medium text-muted-foreground transition-colors hover:bg-muted hover:text-foreground focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring",
              transcriptExpanded && "bg-primary/[0.08] text-primary hover:bg-primary/[0.12] hover:text-primary",
            )}
            onClick={() => setTranscriptExpanded((expanded) => !expanded)}
            aria-expanded={transcriptExpanded}
            aria-controls={transcriptPanelId}
            aria-label={transcriptToggleLabel}
          >
            <Captions className="size-3.5" aria-hidden="true" />
            <span>{transcriptToggleLabel}</span>
          </button>
        ) : null}
      </div>
      {transcriptExpanded && transcriptAvailable ? (
        <section
          id={transcriptPanelId}
          data-testid="voice-reply-transcript"
          className="ml-3 mt-2 w-fit max-w-[min(36rem,calc(100vw-6rem))] border-l-2 border-primary/25 pl-3"
          aria-labelledby={transcriptLabelId}
        >
          <div className="rounded-xl rounded-tl-sm border border-border/70 bg-muted/30 px-3 py-2.5 shadow-sm">
            <div
              id={transcriptLabelId}
              className="mb-1 flex items-center gap-1.5 text-[11px] font-semibold text-primary/80"
            >
              <Captions className="size-3.5" aria-hidden="true" />
              <span>{t(($) => $.message.voice_transcript_label)}</span>
            </div>
            <div className="text-sm leading-6 text-ink">
              <MessageBody
                content={message.content}
                parts={message.parts}
                highlightQuery={highlightQuery}
                sourceMessageId={message.id}
                contentMode="transcript"
              />
            </div>
          </div>
        </section>
      ) : null}
    </div>
  );
}
