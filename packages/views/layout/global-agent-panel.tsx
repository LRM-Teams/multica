"use client";

import { useEffect, useState } from "react";
import { Dialog } from "@base-ui/react/dialog";
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
 * fixed right-side panel so every dashboard page gets "click an agent → see
 * its panel" without owning its own detail slot — mount once here instead of
 * fragmenting a slot into Agents/Squads/Projects/Runtimes/etc.
 *
 * Only ever opens via `useAgentPanelStore` — channels/DM clicks go through
 * `AgentPanelProvider` instead (see `ActorAvatarPanelTrigger`), so this and
 * the in-context panel never both render for the same click.
 *
 * Visual parity (Iris #447 finalization): this must read as the SAME profile
 * panel as the docked one in channels/DM, not a distinct modal drawer. So it
 * slides in from the right at the docked panel's width (ResizablePanel
 * `defaultSize=440` in channels-page), reuses the same AgentSidePanel
 * header+tabs, and uses a TRANSPARENT backdrop (click-outside dismiss, no
 * dimming scrim) so the "overlay vs push" difference stays invisible.
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
  // implicit "done looking"). Without this the fixed panel would linger across
  // pages (open on Agents, still floating on Runtimes). `close` is a stable
  // zustand setter, so this only re-runs on real navigation — not on the
  // open() that set selectedAgentId (that keeps the same pathname).
  useEffect(() => {
    close();
  }, [pathname, close]);

  const liveAgent = agents.find((a) => a.id === selectedAgentId) ?? null;

  // Latch the resolved agent so its content survives the slide-out exit
  // animation: `close()` clears selectedAgentId immediately, so `liveAgent`
  // drops to null while Base UI is still animating the Popup out. Without the
  // latch the panel would slide out empty. Refreshed the instant a new agent
  // resolves (React's sanctioned "adjust state during render" — not an effect,
  // so no extra commit/paint), so re-opening on a different agent swaps content
  // cleanly.
  const [displayed, setDisplayed] = useState<(typeof agents)[number] | null>(null);
  if (liveAgent && liveAgent.id !== displayed?.id) {
    setDisplayed(liveAgent);
  }

  return (
    <Dialog.Root
      open={!!liveAgent}
      onOpenChange={(nextOpen) => {
        if (!nextOpen) close();
      }}
    >
      <Dialog.Portal>
        {/* Transparent backdrop — click-outside dismisses, but no dimming
            scrim: the panel should feel like the docked profile panel, not a
            modal drawer over a darkened page. */}
        <Dialog.Backdrop className="fixed inset-0 z-40" />
        {/* Right slide-in at the docked panel's width. Base UI's
            data-starting-style / data-ending-style drive the enter/exit
            translate on real mount/unmount, so it animates without a manual
            rAF two-frame dance. */}
        <Dialog.Popup className="fixed inset-y-0 right-0 z-50 w-[440px] max-w-[90vw] border-l border-border/30 bg-background shadow-2xl transition-transform duration-200 ease-in-out data-starting-style:translate-x-full data-ending-style:translate-x-full">
          {displayed ? (
            <AgentSidePanel
              agent={displayed}
              currentUserId={currentUserId}
              members={members}
              onClose={close}
            />
          ) : null}
        </Dialog.Popup>
      </Dialog.Portal>
    </Dialog.Root>
  );
}
