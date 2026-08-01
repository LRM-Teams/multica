"use client";

import { useEffect, useState } from "react";
import { Dialog } from "@base-ui/react/dialog";
import { useQuery } from "@tanstack/react-query";
import { useWorkspaceId } from "@multica/core/hooks";
import { useAuthStore } from "@multica/core/auth";
import { resolveActorDisplayName } from "@multica/core/identity";
import { useMemberPanelStore } from "@multica/core/workspace";
import { memberListOptions } from "@multica/core/workspace/queries";
import {
  useAgentPanelStore,
} from "@multica/core/agents/stores";
import type { AgentPanelIdentitySnapshot } from "@multica/core/agents";
import { ResolvedAgentSidePanel } from "../common/resolved-agent-side-panel";
import { useNavigation } from "../navigation";
import { useProfilePanelWidth } from "./use-profile-panel-width";
import { useT } from "../i18n/use-t";

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
 * slides in from the right at the docked panel's width (520, matching the
 * channel/DM side dock), reuses the same AgentSidePanel
 * header+tabs, and uses a TRANSPARENT backdrop (click-outside dismiss, no
 * dimming scrim) so the "overlay vs push" difference stays invisible.
 *
 * LRM-481: left-edge drag resizes (360–640, default 520); width persists in
 * localStorage. Mobile profile uses the page route — no drag here.
 *
 * LRM-292: opens on selectedAgentId; panel body always from GetAgent via
 * ResolvedAgentSidePanel — ListAgents is not consulted.
 */
export function GlobalAgentPanel() {
  const { t } = useT("agents");
  const wsId = useWorkspaceId();
  const { pathname } = useNavigation();
  const currentUserId = useAuthStore((s) => s.user?.id ?? null);
  const selectedAgentId = useAgentPanelStore((s) => s.selectedAgentId);
  const identitySnapshot = useAgentPanelStore((s) => s.identitySnapshot);
  const returnToMemberId = useAgentPanelStore((s) => s.returnToMemberId);
  const close = useAgentPanelStore((s) => s.close);
  const openMember = useMemberPanelStore((s) => s.open);
  const { width, onResizePointerDown } = useProfilePanelWidth();
  const { data: members = [] } = useQuery({
    ...memberListOptions(wsId),
    enabled: !!selectedAgentId || !!returnToMemberId,
  });
  const backLabel = returnToMemberId
    ? resolveActorDisplayName(
        members.find((m) => m.user_id === returnToMemberId) ?? null,
        returnToMemberId,
      )
    : undefined;

  // Peek semantics: a route change dismisses the panel (navigating away is an
  // implicit "done looking"). Without this the fixed panel would linger across
  // pages (open on Agents, still floating on Runtimes). `close` is a stable
  // zustand setter, so this only re-runs on real navigation — not on the
  // open() that set selectedAgentId (that keeps the same pathname).
  useEffect(() => {
    close();
  }, [pathname, close]);

  // Latch the selected id + snapshot so content survives the slide-out exit
  // animation: `close()` clears selectedAgentId immediately, so without the
  // latch the panel would slide out empty. Refreshed when a new agent is selected.
  const [displayedId, setDisplayedId] = useState<string | null>(null);
  const [displayedSnapshot, setDisplayedSnapshot] =
    useState<AgentPanelIdentitySnapshot | null>(null);
  if (selectedAgentId && selectedAgentId !== displayedId) {
    setDisplayedId(selectedAgentId);
    setDisplayedSnapshot(identitySnapshot);
  }

  const open = !!selectedAgentId;
  const panelAgentId = selectedAgentId ?? displayedId;
  const panelSnapshot = selectedAgentId ? identitySnapshot : displayedSnapshot;

  return (
    <Dialog.Root
      open={open}
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
        <Dialog.Popup
          className="fixed inset-y-0 right-0 z-50 max-w-[90vw] border-l border-border/30 bg-background shadow-2xl transition-transform duration-200 ease-in-out motion-reduce:transition-none data-starting-style:translate-x-full data-ending-style:translate-x-full"
          style={{ width }}
          data-testid="global-agent-panel"
        >
          <button
            type="button"
            data-testid="global-agent-panel-resize"
            aria-label={t(($) => $.side_panel.resize_aria)}
            className="absolute inset-y-0 left-0 z-10 w-1.5 cursor-col-resize border-0 bg-transparent p-0 hover:bg-foreground/10 data-[separator=active]:bg-foreground/15"
            onPointerDown={onResizePointerDown}
          />
          {panelAgentId ? (
            <ResolvedAgentSidePanel
              agentId={panelAgentId}
              identitySnapshot={panelSnapshot}
              currentUserId={currentUserId}
              members={members}
              onClose={close}
              onBack={
                returnToMemberId
                  ? () => {
                      const memberId = returnToMemberId;
                      close();
                      openMember(memberId);
                    }
                  : undefined
              }
              backLabel={backLabel}
            />
          ) : null}
        </Dialog.Popup>
      </Dialog.Portal>
    </Dialog.Root>
  );
}
