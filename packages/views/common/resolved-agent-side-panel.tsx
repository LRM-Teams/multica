"use client";

import { useEffect, useRef } from "react";
import { useQuery } from "@tanstack/react-query";
import { ApiError } from "@multica/core/api";
import {
  agentDetailOptions,
  memberProfileOptions,
} from "@multica/core/agents";
import { useWorkspaceId } from "@multica/core/hooks";
import type { MemberWithUser } from "@multica/core/types";
import { Skeleton } from "@multica/ui/components/ui/skeleton";
import { toast } from "sonner";
import { AgentSidePanel } from "../channels/components/agent-side-panel";
import { useT } from "../i18n/use-t";
import { ActorProfileContent } from "./actor-profile-popover";
import { ConversationSidePanelShell } from "./conversation-side-panel-shell";

/** Optional row/message identity for loading chrome while GetAgent resolves. */
export type AgentIdentitySnapshot = {
  name?: string | null;
  display_name?: string | null;
  avatar_url?: string | null;
};

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

/**
 * Opens the #349 agent side panel from an agentId.
 *
 * LRM-292: ListAgents is directory/invite discovery only (LRM-233 still hides
 * channel-only / group managers). Panel body always comes from
 * GET /api/agents/:id — never `agents.find(id)` as an open gate.
 *
 * Resolution:
 * 1. Always GET /api/agents/:id.
 * 2. On 403 → identity-only member profile (basic card).
 * 3. On true failure → toast + explicit error panel (no silent null / LRM-238).
 */
export function ResolvedAgentSidePanel({
  agentId,
  identitySnapshot = null,
  currentUserId,
  members,
  onClose,
  variant = "panel",
}: {
  agentId: string;
  /** Optional name/avatar for loading chrome; not a substitute for GetAgent. */
  identitySnapshot?: AgentIdentitySnapshot | null;
  currentUserId: string | null;
  members: readonly MemberWithUser[];
  onClose: () => void;
  variant?: "panel" | "page";
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
    (detailForbidden
      ? identityIsError || identityFetched
      : detailIsError);

  const toastedRef = useRef(false);
  useEffect(() => {
    toastedRef.current = false;
  }, [agentId]);

  useEffect(() => {
    if (!openFailed || toastedRef.current) return;
    toastedRef.current = true;
    toast.error(t(($) => $.profile_popover.no_permission_toast));
  }, [openFailed, t]);

  if (agent) {
    return (
      <AgentSidePanel
        agent={agent}
        currentUserId={currentUserId}
        members={members}
        onClose={onClose}
        variant={variant}
      />
    );
  }

  if (detailPending || (detailForbidden && identityPending)) {
    const snapshotName =
      identitySnapshot?.display_name || identitySnapshot?.name || null;
    return (
      <ConversationSidePanelShell
        variant={variant}
        onClose={onClose}
        closeAriaLabel={t(($) => $.profile_popover.close_aria)}
        // react-doctor-disable-next-line react-doctor/jsx-no-jsx-as-prop -- shell leading slot
        leading={
          snapshotName ? (
            <p className="min-w-0 truncate text-sm font-semibold">
              {snapshotName}
            </p>
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
