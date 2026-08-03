"use client";

import {
  Mic,
  MicOff,
  Minimize2,
  Phone,
  PhoneOff,
  RotateCcw,
  Volume2,
  VolumeX,
} from "lucide-react";
import { useEffect, type ReactNode } from "react";
import { Button } from "@multica/ui/components/ui/button";
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
  /** LRM-1077: shrunk floating pip; call stays alive. */
  minimized?: boolean;
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
  onMinimize?: () => void;
  onExpand?: () => void;
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

function connectingPhase(phase: VoiceCallControllerPhase): boolean {
  return phase === "idle" ||
    phase === "creating" ||
    phase === "joining";
}

export function VoiceCallPanel({
  open,
  minimized = false,
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
  onMinimize,
  onExpand,
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
  const connecting = connectingPhase(phase);
  const stopFailed = error?.source === "stop";
  const canMute = phase === "connected" || muted;
  // LRM-1077: speakerphone always visible while the call is live (not duplex-only).
  const canSpeakerphone = active;
  const canHangUp =
    phase !== "idle" && phase !== "ending" && phase !== "ended";
  const canMinimize =
    !!onMinimize &&
    phase !== "ended" &&
    !(phase === "failed" && !stopFailed);

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

  const statusLine = (() => {
    switch (phase) {
      case "idle":
      case "creating":
      case "joining":
        return t(($) => $.voice_call.connecting);
      case "connected":
        return t(($) => $.voice_call.in_call);
      case "muted":
        return t(($) => $.voice_call.in_call);
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

  // Lock body scroll while the fullscreen shell is up.
  useEffect(() => {
    if (!open || minimized) return;
    const prev = document.body.style.overflow;
    document.body.style.overflow = "hidden";
    return () => {
      document.body.style.overflow = prev;
    };
  }, [open, minimized]);

  if (!open) return null;

  if (minimized) {
    return (
      <button
        type="button"
        data-testid="voice-call-pip"
        className={cn(
          "fixed bottom-20 right-4 z-[80] flex max-w-[min(100vw-2rem,16rem)] items-center gap-3",
          "rounded-2xl border border-white/10 bg-[#0b0b0c]/95 px-3 py-2.5 text-left text-white shadow-2xl",
          "backdrop-blur-md transition hover:bg-[#161618] sm:bottom-6",
        )}
        aria-label={t(($) => $.voice_call.expand_aria, { name: agentName })}
        onClick={() => onExpand?.()}
      >
        <ActorAvatar
          actorType="agent"
          actorId={agentId}
          size={40}
          showStatusDot
          profileLink={false}
        />
        <span className="min-w-0 flex-1">
          <span className="block truncate text-sm font-medium">{agentName}</span>
          <span className="block truncate text-xs text-white/60 tabular-nums">
            {active ? duration : statusLine}
          </span>
        </span>
        <Phone className="size-4 shrink-0 text-emerald-400" aria-hidden />
      </button>
    );
  }

  return (
    <div
      role="dialog"
      aria-modal="true"
      aria-labelledby="voice-call-title"
      data-testid="voice-call-fullscreen"
      className={cn(
        "fixed inset-0 z-[80] flex flex-col text-white",
        "bg-[radial-gradient(120%_80%_at_50%_0%,#2a2a2e_0%,#0b0b0c_55%)]",
      )}
    >
      {canMinimize && (
        <Button
          type="button"
          variant="ghost"
          size="icon"
          data-testid="voice-call-minimize"
          className="absolute left-3 top-3 z-10 size-9 rounded-lg bg-white/10 text-white hover:bg-white/20 hover:text-white"
          aria-label={t(($) => $.voice_call.minimize_aria)}
          onClick={() => onMinimize?.()}
        >
          <Minimize2 className="size-4" />
        </Button>
      )}

      <div className="flex min-h-0 flex-1 flex-col items-center justify-center px-6 pb-8 pt-16 text-center">
        <div className="relative mb-5">
          <span
            aria-hidden="true"
            className={cn(
              "absolute -inset-3 rounded-[22px] border border-white/15 transition-opacity",
              active ? "opacity-100" : "opacity-0",
            )}
          />
          <div className="overflow-hidden rounded-[18px]">
            <ActorAvatar
              actorType="agent"
              actorId={agentId}
              size={88}
              showStatusDot
              profileLink={false}
            />
          </div>
        </div>

        <h1
          id="voice-call-title"
          className="max-w-full truncate text-[22px] font-semibold tracking-tight"
        >
          {agentName}
        </h1>
        <p
          className={cn(
            "mt-2 min-h-5 text-sm",
            error ? "text-red-300" : "text-white/60",
          )}
          aria-live="polite"
        >
          {connecting
            ? t(($) => $.voice_call.invite_status)
            : statusLine}
        </p>
        {active && (
          <p className="mt-1.5 text-[13px] tabular-nums text-white/55">
            {duration}
          </p>
        )}

        {/* Keep title semantics for a11y / existing tests without crowding the shell */}
        <p className="sr-only">
          {t(($) => $.voice_call.title, { name: agentName })}
        </p>
        <p className="sr-only">{t(($) => $.voice_call.description)}</p>

        {failureMessage && (
          <div
            role="alert"
            className="mt-5 w-full max-w-sm rounded-xl border border-red-400/30 bg-red-500/10 px-3 py-2 text-left text-xs leading-5 text-red-100"
          >
            <p>{failureMessage}</p>
            {error?.providerCode && (
              <p className="mt-1 font-mono text-[11px] text-red-200/80">
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
              "mt-4 block w-full max-w-sm rounded-xl border px-3 py-2 text-left text-xs leading-5",
              toolStatus.status === "error"
                ? "border-red-400/30 bg-red-500/10 text-red-100"
                : "border-white/15 bg-white/5 text-white/80",
            )}
          >
            <p>{toolLabel}</p>
          </output>
        )}

        {mode === "duplex" && active && (
          <p className="mt-2 text-[11px] text-white/45">
            {t(($) => $.voice_call.duplex_mode)}
          </p>
        )}

        {autoplayBlocked && (
          <div className="mt-5 flex w-full max-w-sm items-center gap-3 rounded-xl border border-white/15 bg-white/5 p-2.5 text-left">
            <span className="flex size-8 shrink-0 items-center justify-center rounded-full bg-white/10">
              <Volume2 className="size-4" />
            </span>
            <p className="min-w-0 flex-1 text-xs leading-4 text-white/70">
              {t(($) => $.voice_call.autoplay_blocked, {
                name: agentName,
              })}
            </p>
            <Button
              type="button"
              size="sm"
              className="bg-white text-black hover:bg-white/90"
              onClick={onResumeAudio}
            >
              {t(($) => $.voice_call.resume_audio)}
            </Button>
          </div>
        )}
      </div>

      <div
        data-testid="voice-call-actions"
        className="flex items-end justify-center gap-9 px-6 pb-[max(2.5rem,env(safe-area-inset-bottom))] pt-2"
      >
        {phase === "failed" && !stopFailed ? (
          <>
            <ActionButton
              label={t(($) => $.voice_call.close)}
              tone="chip"
              onClick={onRequestClose}
            >
              <PhoneOff className="size-6" />
            </ActionButton>
            <ActionButton
              label={t(($) => $.voice_call.retry)}
              tone="green"
              onClick={onRetry}
            >
              <RotateCcw className="size-6" />
            </ActionButton>
          </>
        ) : phase === "ended" ? (
          <ActionButton
            label={t(($) => $.voice_call.close)}
            tone="chip"
            onClick={onRequestClose}
          >
            <PhoneOff className="size-6" />
          </ActionButton>
        ) : connecting ? (
          <ActionButton
            label={t(($) => $.voice_call.hang_up)}
            tone="red"
            disabled={!canHangUp}
            onClick={onHangUp}
          >
            <PhoneOff className="size-6" />
          </ActionButton>
        ) : (
          <>
            <ActionButton
              label={
                muted
                  ? t(($) => $.voice_call.unmute)
                  : t(($) => $.voice_call.mute)
              }
              tone="chip"
              pressed={muted}
              disabled={!canMute}
              onClick={onToggleMute}
            >
              {muted
                ? <MicOff className="size-6" />
                : <Mic className="size-6" />}
            </ActionButton>
            {canSpeakerphone && (
              <ActionButton
                label={t(($) => $.voice_call.speakerphone)}
                tone="chip"
                pressed={speakerphone}
                onClick={onToggleSpeakerphone}
              >
                {speakerphone
                  ? <Volume2 className="size-6" />
                  : <VolumeX className="size-6" />}
              </ActionButton>
            )}
            <ActionButton
              label={t(($) => $.voice_call.hang_up)}
              caption={
                stopFailed
                  ? t(($) => $.voice_call.retry_hang_up)
                  : undefined
              }
              tone="red"
              disabled={!canHangUp}
              onClick={onHangUp}
            >
              <PhoneOff className="size-6" />
            </ActionButton>
          </>
        )}
      </div>
    </div>
  );
}

function ActionButton({
  label,
  caption,
  tone,
  pressed,
  disabled,
  onClick,
  children,
}: {
  label: string;
  /** Visible caption under the button; defaults to `label`. */
  caption?: string;
  tone: "red" | "green" | "chip";
  pressed?: boolean;
  disabled?: boolean;
  onClick: () => void;
  children: ReactNode;
}) {
  return (
    <div className="flex w-[72px] flex-col items-center gap-2">
      <Button
        type="button"
        size="icon"
        disabled={disabled}
        aria-label={label}
        aria-pressed={pressed}
        onClick={onClick}
        className={cn(
          "size-16 rounded-full border-0 shadow-none",
          tone === "red" &&
            "bg-[#ef4444] text-white hover:bg-[#ef4444]/90 disabled:bg-[#ef4444]/40",
          tone === "green" &&
            "bg-[#22c55e] text-white hover:bg-[#22c55e]/90",
          tone === "chip" &&
            !pressed &&
            "bg-white/12 text-white hover:bg-white/20",
          tone === "chip" &&
            pressed &&
            "bg-white text-[#111] hover:bg-white/90",
        )}
      >
        {children}
      </Button>
      <span className="text-center text-xs text-white/85">
        {caption ?? label}
      </span>
    </div>
  );
}
