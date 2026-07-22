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
} from "../lib/voice-audio";
import { cancelVoicePlayback, prepareVoicePlayback } from "../lib/voice-playback";

type RecordingState = "idle" | "starting" | "recording" | "transcribing";

export interface VoiceInputButtonProps {
  disabled?: boolean;
  isMobile: boolean;
  playbackScope: string;
  onVoiceSend: (transcript: string, durationMs: number) => boolean;
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
  disabled = false,
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
    setState("transcribing");
    recorder.stop();
    stopStream(streamRef.current);
    streamRef.current = null;
  }, [clearMaxTimer]);

  const processRecording = useCallback(async (blob: Blob, durationMs: number) => {
    try {
      const pcm = await decodeRecording(blob);
      if (pcm.byteLength === 0) {
        cancelVoicePlayback(playbackScope);
        toast.error(t(($) => $.composer.voice_no_speech));
        return;
      }
      const transcript = await api.transcribeVoice(pcm);
      if (!mountedRef.current) return;
      if (!transcript) {
        cancelVoicePlayback(playbackScope);
        toast.error(t(($) => $.composer.voice_no_speech));
        return;
      }
      if (!onVoiceSendRef.current(transcript, durationMs)) {
        cancelVoicePlayback(playbackScope);
        toast.error(t(($) => $.composer.send_failed));
      }
    } catch {
      cancelVoicePlayback(playbackScope);
      if (mountedRef.current) toast.error(t(($) => $.composer.voice_transcription_failed));
    } finally {
      if (mountedRef.current) {
        setElapsedSeconds(0);
        setState("idle");
      }
    }
  }, [playbackScope, t]);

  const startCapture = useCallback(async () => {
    if (disabled || state !== "idle") return;
    prepareVoicePlayback(playbackScope);
    setState("starting");
    try {
      if (
        typeof navigator === "undefined" ||
        !navigator.mediaDevices?.getUserMedia ||
        typeof MediaRecorder === "undefined" ||
        typeof AudioContext === "undefined"
      ) {
        throw new Error("unsupported");
      }
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

  const busy = state === "starting" || state === "transcribing";
  const recording = state === "recording";
  const label = recording
    ? t(($) => $.composer.voice_stop, { seconds: elapsedSeconds })
    : busy
      ? t(($) => $.composer.voice_processing)
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
      title={disabled ? t(($) => $.composer.voice_blocked) : label}
      disabled={(disabled && !recording) || busy}
      onClick={recording ? finishCapture : startCapture}
    >
      {busy ? (
        <LoaderCircle className="size-4 animate-spin" />
      ) : recording ? (
        <>
          <Square className="size-3.5 fill-current" />
          <span className="tabular-nums">0:{String(elapsedSeconds).padStart(2, "0")}</span>
        </>
      ) : (
        <Mic className={cn(isMobile ? "size-5" : "size-4")} />
      )}
    </Button>
  );
}
