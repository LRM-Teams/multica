"use client";

import type { DMPeer } from "@multica/core/dm";
import { Button } from "@multica/ui/components/ui/button";
import { Phone } from "lucide-react";
import { useCallback, useEffect, useRef, useState } from "react";
import { useT } from "../i18n/use-t";
import { useVoiceCallController } from "./use-voice-call-controller";
import { VoiceCallPanel } from "./voice-call-panel";

export interface DmAgentVoiceCallProps {
  workspaceId: string;
  channelId: string;
  peer: DMPeer;
}

const TIMER_PHASES = new Set(["connected", "muted", "reconnecting"]);

export function DmAgentVoiceCall({
  workspaceId,
  channelId,
  peer,
}: DmAgentVoiceCallProps) {
  if (peer.type !== "agent") return null;

  return (
    <AgentVoiceCall
      workspaceId={workspaceId}
      channelId={channelId}
      agentId={peer.id}
      agentName={peer.name}
    />
  );
}

interface AgentVoiceCallProps {
  workspaceId: string;
  channelId: string;
  agentId: string;
  agentName: string;
}

function AgentVoiceCall({
  workspaceId,
  channelId,
  agentId,
  agentName,
}: AgentVoiceCallProps) {
  const { t } = useT("channels");
  // Prefer Duplex so DM calls get web_search / web_fetch + Multica delegate
  // (LRM-1070 / LRM-1131). Controller falls back to Volcengine RTC when Duplex
  // is not configured (isDuplexNotConfigured).
  const controller = useVoiceCallController(workspaceId, {
    preferDuplex: true,
  });
  const [open, setOpen] = useState(false);
  const [minimized, setMinimized] = useState(false);
  const [durationSeconds, setDurationSeconds] = useState(0);
  const connectedAtRef = useRef<number | null>(null);

  useEffect(() => {
    if (!TIMER_PHASES.has(controller.phase)) return;
    if (connectedAtRef.current === null) {
      connectedAtRef.current = Date.now();
    }
    const updateDuration = () => {
      const connectedAt = connectedAtRef.current;
      if (connectedAt === null) return;
      setDurationSeconds(Math.floor((Date.now() - connectedAt) / 1_000));
    };
    const timer = window.setInterval(updateDuration, 1_000);
    return () => window.clearInterval(timer);
  }, [controller.phase]);

  const start = useCallback(() => {
    connectedAtRef.current = null;
    setDurationSeconds(0);
    setMinimized(false);
    setOpen(true);
    void controller.start({
      channel_id: channelId,
      agent_id: agentId,
    }).catch(() => {
      // Controller state carries the localized failure surface.
    });
  }, [agentId, channelId, controller]);

  const hangUp = useCallback(() => {
    void controller.hangUp()
      .then(() => {
        setMinimized(false);
        setOpen(false);
      })
      .catch(() => {
        // A stop failure must keep the panel visible for an explicit retry.
        setMinimized(false);
      });
  }, [controller]);

  const requestClose = useCallback(() => {
    const canCloseWithoutStop =
      controller.phase === "idle" ||
      controller.phase === "ended" ||
      (
        controller.phase === "failed" &&
        controller.error?.source !== "stop"
      );
    if (canCloseWithoutStop) {
      setMinimized(false);
      setOpen(false);
      return;
    }
    hangUp();
  }, [controller.error?.source, controller.phase, hangUp]);

  const toggleMute = useCallback(() => {
    void controller.setMuted(controller.phase !== "muted").catch(() => {
      // Controller state reports media-control failures in the open panel.
    });
  }, [controller]);

  const resumeAudio = useCallback(() => {
    void controller.resumeRemoteAudio().catch(() => {
      // Controller state reports playback failures in the open panel.
    });
  }, [controller]);

  const toggleSpeakerphone = useCallback(() => {
    controller.setSpeakerphone(!controller.speakerphone);
  }, [controller]);

  return (
    <>
      <Button
        type="button"
        variant="ghost"
        size="icon"
        className="size-8"
        aria-label={t(($) => $.voice_call.start_aria, { name: agentName })}
        onClick={start}
      >
        <Phone className="size-4" />
      </Button>
      <VoiceCallPanel
        open={open}
        minimized={minimized}
        agentId={agentId}
        agentName={agentName}
        phase={controller.phase}
        error={controller.error}
        durationSeconds={durationSeconds}
        autoplayBlocked={controller.autoplayBlockedUserId !== null}
        mode={controller.mode}
        toolStatus={controller.toolStatus}
        speakerphone={controller.speakerphone}
        onRequestClose={requestClose}
        onMinimize={() => setMinimized(true)}
        onExpand={() => setMinimized(false)}
        onToggleMute={toggleMute}
        onToggleSpeakerphone={toggleSpeakerphone}
        onHangUp={hangUp}
        onRetry={start}
        onResumeAudio={resumeAudio}
      />
    </>
  );
}
