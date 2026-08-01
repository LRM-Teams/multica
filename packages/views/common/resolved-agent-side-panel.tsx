"use client";

import { useEffect, useRef } from "react";
import { useQuery } from "@tanstack/react-query";
import { ApiError } from "@multica/core/api";
import {
  agentDetailOptions,
  memberProfileOptions,
  type AgentPanelIdentitySnapshot,
} from "@multica/core/agents";
import { useWorkspaceId } from "@multica/core/hooks";
import type { MemberWithUser } from "@multica/core/types";
import { Skeleton } from "@multica/ui/components/ui/skeleton";
import { showErrorToast } from "@multica/ui/lib/error-toast";
import { AgentSidePanel } from "../channels/components/agent-side-panel";
import { useT } from "../i18n/use-t";
import { ActorProfileContent } from "./actor-profile-popover";
import { ConversationSidePanelShell } from "./conversation-side-panel-shell";

const PANEL_LOADING_LEADING = (
  <div className="flex items-center gap-2.5">
    <Skeleton className="size-8 rounded-full" />
    <Skeleton className="h-4 w-28" />
  </div>
);

const PANEL_LOADING_BODY = (
  <div className="space-y-3 p-4">
    <Skeleton className="h-3 w-full" />
    <Skeleton className="h-3 w-4/5" />
    <Skeleton className="h-3 w-2/3" />
  </div>
);

function snapshotLabel(snapshot: AgentPanelIdentitySnapshot | null | undefined): string | null {
  if (!snapshot) return null;
  const label = (snapshot.display_name || snapshot.name || "").trim();
  return label || null;
}

/**
 * Resolves an agent panel by id (LRM-292).
 *
 * Contract:
 * - Callers open with `agentId` (+ optional identity snapshot from the row).
 * - Panel body always comes from GET /api/agents/:id.
 * - ListAgents is NOT consulted and is NOT an open gate (Frank / LRM-288 follow-up).
 * - 403 → identity-only member profile (sensitive blocks gated by profile_access).
 * - True failure → explicit toast + error panel (LRM-238: no silent no-op).
 */
export function ResolvedAgentSidePanel({
  agentId,
  identitySnapshot = null,
  currentUserId,
  members,
  onClose,
  variant = "panel",
  onBack,
  backLabel,
  doneLabel,
}: {
  agentId: string;
  /** Optional row-level identity for optimistic chrome while GetAgent loads. */
  identitySnapshot?: AgentPanelIdentitySnapshot | null;
  currentUserId: string | null;
  members: readonly MemberWithUser[];
  onClose: () => void;
  variant?: "panel" | "page";
  /** LRM-877 — pop back to human Profile (Dock Stack). */
  onBack?: () => void;
  backLabel?: string;
  doneLabel?: string;
}) {
  const { t } = useT("channels");
  const wsId = useWorkspaceId();

  const {
    data: agent,
    isPending: detailPending,
    isError: detailIsError,
    error: detailError,
  } = useQuery({
    ...agentDetailOptions(wsId, agentId),
    enabled: !!agentId,
  });

  const detailForbidden =
    detailIsError &&
    detailError instanceof ApiError &&
    detailError.status === 403;

  const {
    data: identityProfile,
    isPending: identityPending,
    isError: identityIsError,
    isFetched: identityFetched,
  } = useQuery({
    ...memberProfileOptions(wsId, "agent", agentId),
    enabled: !!agentId && detailForbidden,
  });

  const openFailed =
    !agent &&
    !detailPending &&
    !(detailForbidden && identityPending) &&
    !(detailForbidden && identityProfile) &&
    (detailForbidden ? identityIsError || identityFetched : detailIsError);

  const toastedRef = useRef(false);
  useEffect(() => {
    toastedRef.current = false;
  }, [agentId]);

  useEffect(() => {
    if (!openFailed || toastedRef.current) return;
    toastedRef.current = true;
    showErrorToast(t(($) => $.profile_popover.no_permission_toast));
  }, [openFailed, t]);

  if (agent) {
    return (
      <AgentSidePanel
        agent={agent}
        currentUserId={currentUserId}
        members={members}
        onClose={onClose}
        variant={variant}
        onBack={onBack}
        backLabel={backLabel}
        doneLabel={doneLabel}
      />
    );
  }

  if (detailPending || (detailForbidden && identityPending)) {
    const optimisticName = snapshotLabel(identitySnapshot);
    return (
      <ConversationSidePanelShell
        variant={variant}
        onClose={onClose}
        closeAriaLabel={t(($) => $.profile_popover.close_aria)}
        // react-doctor-disable-next-line react-doctor/jsx-no-jsx-as-prop -- shell leading slot; optimistic name or skeleton
        leading={
          optimisticName ? (
            <p className="min-w-0 truncate text-sm font-semibold">{optimisticName}</p>
          ) : (
            PANEL_LOADING_LEADING
          )
        }
      >
        {PANEL_LOADING_BODY}
      </ConversationSidePanelShell>
    );
  }

  if (detailForbidden && identityProfile) {
    const identityName =
      identityProfile.display_name || identityProfile.name;
    return (
      <ConversationSidePanelShell
        variant={variant}
        onClose={onClose}
        closeAriaLabel={t(($) => $.profile_popover.close_aria)}
        // react-doctor-disable-next-line react-doctor/jsx-no-jsx-as-prop -- shell leading slot; name is a string leaf
        leading={
          <p className="min-w-0 truncate text-sm font-semibold">
            {identityName}
          </p>
        }
      >
        <div className="min-h-0 flex-1 overflow-y-auto">
          <ActorProfileContent memberType="agent" memberId={agentId} />
        </div>
      </ConversationSidePanelShell>
    );
  }

  if (openFailed) {
    return (
      <ConversationSidePanelShell
        variant={variant}
        onClose={onClose}
        closeAriaLabel={t(($) => $.profile_popover.close_aria)}
        // react-doctor-disable-next-line react-doctor/jsx-no-jsx-as-prop -- shell leading slot; static error title
        leading={
          <p className="min-w-0 truncate text-sm font-semibold">
            {t(($) => $.profile_popover.agent_unavailable)}
          </p>
        }
      >
        <div className="p-4 text-xs text-muted-foreground">
          {t(($) => $.profile_popover.no_permission_toast)}
        </div>
      </ConversationSidePanelShell>
    );
  }

  return null;
}
