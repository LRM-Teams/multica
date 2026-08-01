"use client";

import {
  Mic,
  MicOff,
  PhoneOff,
  RotateCcw,
  Volume2,
  VolumeX,
  X,
} from "lucide-react";
import { Button } from "@multica/ui/components/ui/button";
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogTitle,
} from "@multica/ui/components/ui/dialog";
import { cn } from "@multica/ui/lib/utils";
import { ActorAvatar } from "../common/actor-avatar";
import { useT } from "../i18n/use-t";
import type {
  VoiceCallControllerError,
  VoiceCallControllerPhase,
  VoiceCallMode,
  VoiceCallToolStatus,
} from "./use-voice-call-controller";
import { formatVoiceCallDuration } from "./voice-call-format";

export interface VoiceCallPanelProps {
  open: boolean;
  agentId: string;
  agentName: string;
  phase: VoiceCallControllerPhase;
  error: VoiceCallControllerError | null;
  durationSeconds: number;
  autoplayBlocked: boolean;
  mode: VoiceCallMode;
  toolStatus: VoiceCallToolStatus | null;
  speakerphone: boolean;
  onRequestClose: () => void;
  onToggleMute: () => void;
  onToggleSpeakerphone: () => void;
  onHangUp: () => void;
  onRetry: () => void;
  onResumeAudio: () => void;
}

function activeCallPhase(phase: VoiceCallControllerPhase): boolean {
  return phase === "connected" ||
    phase === "muted" ||
    phase === "reconnecting";
}

export function VoiceCallPanel({
  open,
  agentId,
  agentName,
  phase,
  error,
  durationSeconds,
  autoplayBlocked,
  mode,
  toolStatus,
  speakerphone,
  onRequestClose,
  onToggleMute,
  onToggleSpeakerphone,
  onHangUp,
  onRetry,
  onResumeAudio,
}: VoiceCallPanelProps) {
  const { t } = useT("channels");
  const duration = formatVoiceCallDuration(durationSeconds);
  const muted = phase === "muted";
  const active = activeCallPhase(phase);
  const stopFailed = error?.source === "stop";
  const canMute = phase === "connected" || muted;
  const canSpeakerphone = active && mode === "duplex";
  const canHangUp =
    phase !== "idle" && phase !== "ending" && phase !== "ended";

  const toolLabel = toolStatus
    ? t(($) => {
      switch (toolStatus.status) {
        case "started":
          return $.voice_call.tool_running;
        case "done":
          return $.voice_call.tool_done;
        case "error":
          return $.voice_call.tool_error;
      }
    }, { name: toolStatus.name })
    : null;

  const status = (() => {
    switch (phase) {
      case "idle":
      case "creating":
      case "joining":
        return t(($) => $.voice_call.connecting);
      case "connected":
        return t(($) => $.voice_call.connected, { duration });
      case "muted":
        return t(($) => $.voice_call.muted, { duration });
      case "reconnecting":
        return t(($) => $.voice_call.reconnecting);
      case "ending":
        return t(($) => $.voice_call.ending);
      case "ended":
        return t(($) => $.voice_call.ended);
      case "failed":
        return t(($) => $.voice_call.failed);
    }
  })();

  const failureMessage = error
    ? t(($) => {
      switch (error.source) {
        case "create":
          return $.voice_call.create_failed;
        case "media":
          return error.code === "insecure_context"
            ? $.composer.voice_secure_context_required
            : $.voice_call.media_failed;
        case "stop":
          return $.voice_call.stop_failed;
        case "server":
          return error.code === "provider_activation_timeout"
            ? $.voice_call.activation_timeout
            : $.voice_call.server_failed;
      }
    })
    : null;

  return (
    <Dialog
      open={open}
      onOpenChange={(nextOpen) => {
        if (!nextOpen) onRequestClose();
      }}
    >
      <DialogContent
        showCloseButton={false}
        className="overflow-hidden p-0 sm:max-w-[380px]"
      >
        <div className="relative flex min-h-72 flex-col items-center overflow-hidden bg-gradient-to-b from-primary/10 via-background to-background px-6 pb-5 pt-8 text-center">
          <Button
            type="button"
            variant="ghost"
            size="icon"
            className="absolute right-3 top-3 rounded-full text-muted-foreground"
            aria-label={t(($) => $.voice_call.close)}
            onClick={onRequestClose}
          >
            <X className="size-4" />
          </Button>

          <div className="relative mb-4">
            <span
              aria-hidden="true"
              className={cn(
                "absolute -inset-2 rounded-full border border-primary/20 transition-opacity",
                active ? "opacity-100" : "opacity-0",
              )}
            />
            <span
              aria-hidden="true"
              className={cn(
                "absolute -inset-4 rounded-full border border-primary/10 transition-opacity",
                active ? "opacity-100 motion-safe:animate-pulse" : "opacity-0",
              )}
            />
            <ActorAvatar
              actorType="agent"
              actorId={agentId}
              size={80}
              showStatusDot
              profileLink={false}
            />
          </div>

          <DialogTitle className="max-w-full truncate text-lg font-semibold leading-6">
            {t(($) => $.voice_call.title, { name: agentName })}
          </DialogTitle>
          <DialogDescription>
            {t(($) => $.voice_call.description)}
          </DialogDescription>
          <p
            className={cn(
              "mt-2 min-h-5 text-sm tabular-nums",
              error ? "text-destructive" : "text-foreground/75",
            )}
            aria-live="polite"
          >
            {status}
          </p>

          {failureMessage && (
            <div
              role="alert"
              className="mt-4 w-full rounded-lg border border-destructive/20 bg-destructive/5 px-3 py-2 text-left text-xs leading-5 text-destructive"
            >
              <p>{failureMessage}</p>
              {error?.providerCode && (
                <p className="mt-1 font-mono text-[11px] text-destructive/80">
                  {t(($) => $.voice_call.rtc_diagnostic_code, {
                    code: error.providerCode,
                  })}
                </p>
              )}
            </div>
          )}

          {toolStatus && toolLabel && (
            <output
              className={cn(
                "mt-4 block w-full rounded-lg border px-3 py-2 text-left text-xs leading-5",
                toolStatus.status === "error"
                  ? "border-destructive/20 bg-destructive/5 text-destructive"
                  : "border-primary/20 bg-primary/5 text-foreground/80",
              )}
            >
              <p>{toolLabel}</p>
            </output>
          )}

          {mode === "duplex" && active && (
            <p className="mt-2 text-[11px] text-muted-foreground">
              {t(($) => $.voice_call.duplex_mode)}
            </p>
          )}

          {autoplayBlocked && (
            <div className="mt-4 flex w-full items-center gap-3 rounded-lg border bg-background/90 p-2.5 text-left shadow-sm">
              <span className="flex size-8 shrink-0 items-center justify-center rounded-full bg-primary/10 text-primary">
                <Volume2 className="size-4" />
              </span>
              <p className="min-w-0 flex-1 text-xs leading-4 text-muted-foreground">
                {t(($) => $.voice_call.autoplay_blocked, {
                  name: agentName,
                })}
              </p>
              <Button type="button" size="sm" onClick={onResumeAudio}>
                {t(($) => $.voice_call.resume_audio)}
              </Button>
            </div>
          )}
        </div>

        <div className="flex min-h-24 items-center justify-center gap-6 border-t bg-muted/25 px-6 py-4">
          {phase === "failed" && !stopFailed ? (
            <>
              <Button type="button" variant="outline" onClick={onRequestClose}>
                {t(($) => $.voice_call.close)}
              </Button>
              <Button type="button" onClick={onRetry}>
                <RotateCcw className="size-4" />
                {t(($) => $.voice_call.retry)}
              </Button>
            </>
          ) : phase === "ended" ? (
            <Button type="button" onClick={onRequestClose}>
              {t(($) => $.voice_call.close)}
            </Button>
          ) : (
            <>
              <div className="flex flex-col items-center gap-1.5">
                <Button
                  type="button"
                  variant="secondary"
                  size="icon-lg"
                  className="size-12 rounded-full"
                  aria-label={
                    muted
                      ? t(($) => $.voice_call.unmute)
                      : t(($) => $.voice_call.mute)
                  }
                  disabled={!canMute}
                  onClick={onToggleMute}
                >
                  {muted
                    ? <MicOff className="size-5" />
                    : <Mic className="size-5" />}
                </Button>
                <span className="text-[11px] text-muted-foreground">
                  {muted
                    ? t(($) => $.voice_call.unmute)
                    : t(($) => $.voice_call.mute)}
                </span>
              </div>
              {canSpeakerphone && (
                <div className="flex flex-col items-center gap-1.5">
                  <Button
                    type="button"
                    variant="secondary"
                    size="icon-lg"
                    className="size-12 rounded-full"
                    aria-label={
                      speakerphone
                        ? t(($) => $.voice_call.speakerphone_off)
                        : t(($) => $.voice_call.speakerphone_on)
                    }
                    onClick={onToggleSpeakerphone}
                  >
                    {speakerphone
                      ? <Volume2 className="size-5" />
                      : <VolumeX className="size-5" />}
                  </Button>
                  <span className="text-[11px] text-muted-foreground">
                    {speakerphone
                      ? t(($) => $.voice_call.speakerphone_off)
                      : t(($) => $.voice_call.speakerphone_on)}
                  </span>
                </div>
              )}
              <div className="flex flex-col items-center gap-1.5">
                <Button
                  type="button"
                  size="icon-lg"
                  className="size-12 rounded-full bg-destructive text-white hover:bg-destructive/90"
                  aria-label={t(($) => $.voice_call.hang_up)}
                  disabled={!canHangUp}
                  onClick={onHangUp}
                >
                  <PhoneOff className="size-5" />
                </Button>
                <span className="text-[11px] text-muted-foreground">
                  {stopFailed
                    ? t(($) => $.voice_call.retry_hang_up)
                    : t(($) => $.voice_call.hang_up)}
                </span>
              </div>
            </>
          )}
        </div>
      </DialogContent>
    </Dialog>
  );
}
