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
  const controller = useVoiceCallController(workspaceId);
  const [open, setOpen] = useState(false);
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
      .then(() => setOpen(false))
      .catch(() => {
        // A stop failure must keep the panel visible for an explicit retry.
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
        onToggleMute={toggleMute}
        onToggleSpeakerphone={toggleSpeakerphone}
        onHangUp={hangUp}
        onRetry={start}
        onResumeAudio={resumeAudio}
      />
    </>
  );
}
