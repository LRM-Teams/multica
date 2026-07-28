"use client";

import { useCallback, useEffect, useRef, useState } from "react";
import { LoaderCircle, Mic, Square } from "lucide-react";
import { api } from "@multica/core/api";
import { Button } from "@multica/ui/components/ui/button";
import { cn } from "@multica/ui/lib/utils";
import { toast } from "sonner";
import { useT } from "../../i18n/use-t";
import {
  downmixAudioBuffer,
  encodeVoicePCM,
  MAX_VOICE_RECORDING_MS,
  type VoiceRecordingAttachment,
} from "../lib/voice-audio";
import { deliverVoiceRecording } from "../lib/voice-recording-delivery";
import { voiceCaptureUnavailableReason } from "../lib/voice-capture";
import { cancelVoicePlayback, prepareVoicePlayback } from "../lib/voice-playback";

type RecordingState = "idle" | "starting" | "recording" | "uploading";

export interface VoiceInputButtonProps {
  channelId: string;
  disabled?: boolean;
  /**
   * Why the mic is blocked, when the caller knows something more specific than
   * the generic reason (#838: an unsent recording is waiting).
   *
   * `voiceDisabled` is currently ORed from several causes and the default copy
   * only describes some of them — telling a user with an empty composer to
   * "clear the current text and attachments" sends them somewhere that does
   * nothing. A caller that knows the actual cause passes it here.
   *
   * Deliberately a plain resolved string and NOT a visible surface: #857 owns
   * the real fix (a visible `role="status"` line covering all causes on
   * touch/mouse/keyboard/SR). This only stops the existing title from lying
   * about the state #838 introduced, and #857 replaces it wholesale.
   */
  blockedReason?: string;
  isMobile: boolean;
  playbackScope: string;
  onVoiceSend: (
    durationMs: number,
    attachment: VoiceRecordingAttachment,
  ) => boolean;
}

function preferredRecordingMimeType(): string | undefined {
  if (typeof MediaRecorder === "undefined") return undefined;
  return [
    "audio/webm;codecs=opus",
    "audio/mp4;codecs=mp4a.40.2",
    "audio/ogg;codecs=opus",
  ].find((type) => MediaRecorder.isTypeSupported(type));
}

function stopStream(stream: MediaStream | null): void {
  stream?.getTracks().forEach((track) => track.stop());
}

async function decodeRecording(blob: Blob): Promise<ArrayBuffer> {
  const context = new AudioContext();
  try {
    const decoded = await context.decodeAudioData(await blob.arrayBuffer());
    return encodeVoicePCM(downmixAudioBuffer(decoded), decoded.sampleRate);
  } finally {
    await context.close();
  }
}

export function VoiceInputButton({
  channelId,
  disabled = false,
  blockedReason,
  isMobile,
  playbackScope,
  onVoiceSend,
}: VoiceInputButtonProps) {
  const { t } = useT("channels");
  const [state, setState] = useState<RecordingState>("idle");
  const [elapsedSeconds, setElapsedSeconds] = useState(0);
  const recorderRef = useRef<MediaRecorder | null>(null);
  const streamRef = useRef<MediaStream | null>(null);
  const chunksRef = useRef<Blob[]>([]);
  const startedAtRef = useRef(0);
  const maxTimerRef = useRef<ReturnType<typeof setTimeout> | null>(null);
  const mountedRef = useRef(true);
  const onVoiceSendRef = useRef(onVoiceSend);
  onVoiceSendRef.current = onVoiceSend;

  const clearMaxTimer = useCallback(() => {
    if (maxTimerRef.current) clearTimeout(maxTimerRef.current);
    maxTimerRef.current = null;
  }, []);

  const finishCapture = useCallback(() => {
    const recorder = recorderRef.current;
    if (!recorder || recorder.state === "inactive") return;
    clearMaxTimer();
    setState("uploading");
    recorder.stop();
    stopStream(streamRef.current);
    streamRef.current = null;
  }, [clearMaxTimer]);

  const processRecording = useCallback(async (blob: Blob, durationMs: number) => {
    let uploadedAttachmentId: string | null = null;
    try {
      const pcm = await decodeRecording(blob);
      if (pcm.byteLength === 0) {
        cancelVoicePlayback(playbackScope);
        toast.error(t(($) => $.composer.voice_no_speech));
        return;
      }
      const { attachment } = await deliverVoiceRecording(pcm, channelId);
      uploadedAttachmentId = attachment.id;
      if (!mountedRef.current) {
        if (attachment.id) await api.deleteAttachment(attachment.id).catch(() => undefined);
        uploadedAttachmentId = null;
        return;
      }
      if (!onVoiceSendRef.current(durationMs, attachment)) {
        if (attachment.id) await api.deleteAttachment(attachment.id).catch(() => undefined);
        uploadedAttachmentId = null;
        cancelVoicePlayback(playbackScope);
        toast.error(t(($) => $.composer.send_failed));
      }
    } catch {
      if (uploadedAttachmentId) {
        await api.deleteAttachment(uploadedAttachmentId).catch(() => undefined);
      }
      cancelVoicePlayback(playbackScope);
      if (mountedRef.current) toast.error(t(($) => $.composer.voice_upload_failed));
    } finally {
      if (mountedRef.current) {
        setElapsedSeconds(0);
        setState("idle");
      }
    }
  }, [channelId, playbackScope, t]);

  const startCapture = useCallback(async () => {
    if (disabled || state !== "idle") return;
    const unavailableReason = voiceCaptureUnavailableReason();
    if (unavailableReason) {
      toast.error(t(($) => unavailableReason === "insecure-context"
        ? $.composer.voice_secure_context_required
        : $.composer.voice_unavailable));
      return;
    }
    prepareVoicePlayback(playbackScope);
    setState("starting");
    try {
      const stream = await navigator.mediaDevices.getUserMedia({
        audio: { echoCancellation: true, noiseSuppression: true, autoGainControl: true },
      });
      if (!mountedRef.current) {
        stopStream(stream);
        return;
      }
      const mimeType = preferredRecordingMimeType();
      const recorder = mimeType ? new MediaRecorder(stream, { mimeType }) : new MediaRecorder(stream);
      recorderRef.current = recorder;
      streamRef.current = stream;
      chunksRef.current = [];
      startedAtRef.current = Date.now();
      recorder.ondataavailable = (event) => {
        if (event.data.size > 0) chunksRef.current.push(event.data);
      };
      recorder.onerror = () => {
        clearMaxTimer();
        cancelVoicePlayback(playbackScope);
        recorder.onstop = null;
        recorderRef.current = null;
        chunksRef.current = [];
        stopStream(streamRef.current);
        streamRef.current = null;
        if (mountedRef.current) {
          setState("idle");
          toast.error(t(($) => $.composer.voice_recording_failed));
        }
      };
      recorder.onstop = () => {
        const durationMs = Math.min(MAX_VOICE_RECORDING_MS, Date.now() - startedAtRef.current);
        const blob = new Blob(chunksRef.current, { type: recorder.mimeType });
        chunksRef.current = [];
        recorderRef.current = null;
        void processRecording(blob, durationMs);
      };
      recorder.start(250);
      setElapsedSeconds(0);
      setState("recording");
      maxTimerRef.current = setTimeout(finishCapture, MAX_VOICE_RECORDING_MS);
    } catch (error) {
      cancelVoicePlayback(playbackScope);
      recorderRef.current = null;
      chunksRef.current = [];
      stopStream(streamRef.current);
      streamRef.current = null;
      if (!mountedRef.current) return;
      setState("idle");
      if (error instanceof DOMException && error.name === "NotAllowedError") {
        toast.error(t(($) => $.composer.voice_permission_denied));
      } else {
        toast.error(t(($) => $.composer.voice_unavailable));
      }
    }
  }, [clearMaxTimer, disabled, finishCapture, playbackScope, processRecording, state, t]);

  useEffect(() => {
    if (state !== "recording") return;
    const interval = window.setInterval(() => {
      setElapsedSeconds(Math.min(60, Math.floor((Date.now() - startedAtRef.current) / 1000)));
    }, 250);
    return () => window.clearInterval(interval);
  }, [state]);

  useEffect(() => {
    mountedRef.current = true;
    return () => {
      mountedRef.current = false;
      cancelVoicePlayback(playbackScope);
      clearMaxTimer();
      const recorder = recorderRef.current;
      recorderRef.current = null;
      if (recorder?.state === "recording") {
        recorder.ondataavailable = null;
        recorder.onerror = null;
        recorder.onstop = null;
        recorder.stop();
      }
      stopStream(streamRef.current);
      streamRef.current = null;
    };
  }, [clearMaxTimer, playbackScope]);

  const busy = state === "starting" || state === "uploading";
  const recording = state === "recording";
  const label = recording
    ? t(($) => $.composer.voice_stop, { seconds: elapsedSeconds })
    : busy
      ? t(($) => $.composer.voice_uploading)
      : t(($) => $.composer.voice_start);

  return (
    <Button
      type="button"
      variant="ghost"
      size={recording ? "sm" : "icon"}
      className={cn(
        isMobile ? "min-h-10" : "h-8",
        recording && "gap-1.5 bg-destructive/10 text-destructive hover:bg-destructive/15 hover:text-destructive",
        !recording && (isMobile ? "w-10" : "w-8"),
      )}
      aria-label={label}
      aria-live="polite"
      aria-busy={busy}
      title={disabled ? blockedReason ?? t(($) => $.composer.voice_blocked) : label}
      disabled={(disabled && !recording) || busy}
      onClick={recording ? finishCapture : startCapture}
    >
      {busy ? (
        <LoaderCircle
          className="size-4 animate-spin motion-reduce:animate-none"
          aria-hidden="true"
        />
      ) : recording ? (
        <>
          <Square className="size-3.5 fill-current" aria-hidden="true" />
          <span className="tabular-nums">0:{String(elapsedSeconds).padStart(2, "0")}</span>
        </>
      ) : (
        <Mic className={cn(isMobile ? "size-5" : "size-4")} aria-hidden="true" />
      )}
    </Button>
  );
}
