"use client";

import { useEffect, useRef } from "react";
import { useQuery } from "@tanstack/react-query";
import { ApiError } from "@multica/core/api";
import {
  agentDetailOptions,
  memberProfileOptions,
} from "@multica/core/agents";
import { useWorkspaceId } from "@multica/core/hooks";
import type { Agent, MemberWithUser } from "@multica/core/types";
import { Skeleton } from "@multica/ui/components/ui/skeleton";
import { toast } from "sonner";
import { AgentSidePanel } from "../channels/components/agent-side-panel";
import { useT } from "../i18n/use-t";
import { ActorProfileContent } from "./actor-profile-popover";
import { ConversationSidePanelShell } from "./conversation-side-panel-shell";

const EMPTY_AGENTS: readonly Agent[] = [];

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
 * Resolves an agent by id for the #349 side panel when the actor may be absent
 * from ListAgents (channel-only discovery — group managers / LRM-233).
 *
 * Resolution order (LRM-288):
 * 1. Prefer the workspace agent list cache (no extra fetch).
 * 2. Fall back to GET /api/agents/:id (group managers are still readable).
 * 3. On 403, open the identity-only member profile (basic card, sensitive
 *    blocks gated by profile_access) — never a silent no-op.
 * 4. On true failure (404 / network / identity profile also fails), toast
 *    explicitly and show an error panel (close via shell X — no silent null).
 */
export function ResolvedAgentSidePanel({
  agentId,
  agents = EMPTY_AGENTS,
  currentUserId,
  members,
  onClose,
  variant = "panel",
}: {
  agentId: string;
  /** Workspace ListAgents cache; may omit channel-only agents. */
  agents?: readonly Agent[];
  currentUserId: string | null;
  members: readonly MemberWithUser[];
  onClose: () => void;
  variant?: "panel" | "page";
}) {
  const { t } = useT("channels");
  const wsId = useWorkspaceId();
  const listAgent = agents.find((agent) => agent.id === agentId) ?? null;

  const {
    data: detailAgent,
    isPending: detailPending,
    isError: detailIsError,
    error: detailError,
  } = useQuery({
    ...agentDetailOptions(wsId, agentId),
    enabled: !!agentId && !listAgent,
  });

  const agent = listAgent ?? detailAgent ?? null;
  const detailForbidden =
    !listAgent &&
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
    return (
      <ConversationSidePanelShell
        variant={variant}
        onClose={onClose}
        closeAriaLabel={t(($) => $.profile_popover.close_aria)}
        leading={PANEL_LOADING_LEADING}
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
