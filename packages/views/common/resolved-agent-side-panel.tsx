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
 *    explicitly and close.
 */
export function ResolvedAgentSidePanel({
  agentId,
  agents = [],
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

  const detailQuery = useQuery({
    ...agentDetailOptions(wsId, agentId),
    enabled: !!agentId && !listAgent,
  });

  const agent = listAgent ?? detailQuery.data ?? null;
  const detailForbidden =
    !listAgent &&
    detailQuery.isError &&
    detailQuery.error instanceof ApiError &&
    detailQuery.error.status === 403;

  const identityQuery = useQuery({
    ...memberProfileOptions(wsId, "agent", agentId),
    enabled: !!agentId && detailForbidden,
  });

  const toastedRef = useRef(false);
  useEffect(() => {
    toastedRef.current = false;
  }, [agentId]);

  useEffect(() => {
    if (agent || detailQuery.isPending || identityQuery.isPending) return;
    if (detailForbidden) {
      if (identityQuery.data) return;
      if (!identityQuery.isError && !identityQuery.isFetched) return;
    } else if (!detailQuery.isError) {
      return;
    }
    if (toastedRef.current) return;
    toastedRef.current = true;
    toast.error(t(($) => $.profile_popover.no_permission_toast));
    onClose();
  }, [
    agent,
    detailForbidden,
    detailQuery.isError,
    detailQuery.isPending,
    identityQuery.data,
    identityQuery.isError,
    identityQuery.isFetched,
    identityQuery.isPending,
    onClose,
    t,
  ]);

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

  if (
    detailQuery.isPending ||
    (detailForbidden && identityQuery.isPending)
  ) {
    return (
      <ConversationSidePanelShell
        variant={variant}
        onClose={onClose}
        closeAriaLabel={t(($) => $.profile_popover.close_aria)}
        leading={
          <div className="flex items-center gap-2.5">
            <Skeleton className="size-8 rounded-full" />
            <Skeleton className="h-4 w-28" />
          </div>
        }
      >
        <div className="space-y-3 p-4">
          <Skeleton className="h-3 w-full" />
          <Skeleton className="h-3 w-4/5" />
          <Skeleton className="h-3 w-2/3" />
        </div>
      </ConversationSidePanelShell>
    );
  }

  if (detailForbidden && identityQuery.data) {
    return (
      <ConversationSidePanelShell
        variant={variant}
        onClose={onClose}
        closeAriaLabel={t(($) => $.profile_popover.close_aria)}
        leading={
          <p className="min-w-0 truncate text-sm font-semibold">
            {identityQuery.data.display_name || identityQuery.data.name}
          </p>
        }
      >
        <div className="min-h-0 flex-1 overflow-y-auto">
          <ActorProfileContent memberType="agent" memberId={agentId} />
        </div>
      </ConversationSidePanelShell>
    );
  }

  // Toast + onClose run in the effect; keep the slot empty while it fires.
  return null;
}
