"use client";

import { useEffect } from "react";
import { useQuery } from "@tanstack/react-query";
import { useWorkspaceId } from "@multica/core/hooks";
import { useAuthStore } from "@multica/core/auth";
import { agentListOptions, memberListOptions } from "@multica/core/workspace/queries";
import { useAgentPanelStore } from "@multica/core/agents/stores";
import { AgentSidePanel } from "../channels/components/agent-side-panel";
import { useNavigation } from "../navigation";

/**
 * Fallback host for the #349 agent side panel (see agent-panel-context.tsx
 * for the primary, in-context mechanism used by channels/DM). Renders as a
 * fixed overlay so every dashboard page gets "click an agent → see its
 * panel" without owning its own detail slot — mount once here instead of
 * fragmenting a slot into Agents/Squads/Projects/Runtimes/etc.
 *
 * Only ever opens via `useAgentPanelStore` — channels/DM clicks go through
 * `AgentPanelProvider` instead (see `ActorAvatarPanelTrigger`), so this and
 * the in-context panel never both render for the same click.
 */
export function GlobalAgentPanel() {
  const wsId = useWorkspaceId();
  const { pathname } = useNavigation();
  const currentUserId = useAuthStore((s) => s.user?.id ?? null);
  const selectedAgentId = useAgentPanelStore((s) => s.selectedAgentId);
  const close = useAgentPanelStore((s) => s.close);
  const { data: agents = [] } = useQuery({
    ...agentListOptions(wsId),
    enabled: !!selectedAgentId,
  });
  const { data: members = [] } = useQuery({
    ...memberListOptions(wsId),
    enabled: !!selectedAgentId,
  });

  // Peek semantics: a route change dismisses the panel (navigating away is an
  // implicit "done looking"). Without this the fixed overlay would linger
  // across pages (open on Agents, still floating on Runtimes). `close` is a
  // stable zustand setter, so this only re-runs on real navigation — not on
  // the open() that set selectedAgentId (that keeps the same pathname).
  useEffect(() => {
    close();
  }, [pathname, close]);

  if (!selectedAgentId) return null;
  const agent = agents.find((a) => a.id === selectedAgentId);
  if (!agent) return null;

  return (
    <>
      {/* Click-outside backdrop — peek panels dismiss on outside click.
          Transparent so it doesn't dim the page; sits just under the panel. */}
      <div
        className="fixed inset-0 z-40"
        aria-hidden="true"
        onClick={close}
      />
      <div className="fixed inset-y-0 right-0 z-50 w-[380px] max-w-[90vw] shadow-2xl">
        <AgentSidePanel
          agent={agent}
          currentUserId={currentUserId}
          members={members}
          onClose={close}
        />
      </div>
    </>
  );
}
