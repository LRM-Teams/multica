"use client";

import { useEffect, useState } from "react";
import { Dialog } from "@base-ui/react/dialog";
import { useMemberPanelStore } from "@multica/core/members/stores";
import { useAgentPanelStore } from "@multica/core/agents/stores";
import type { MemberPanelIdentitySnapshot } from "@multica/core/members";
import { HumanMemberSidePanel } from "../members/human-member-side-panel";
import { useNavigation } from "../navigation";
import { useProfilePanelWidth } from "./use-profile-panel-width";
import { useT } from "../i18n/use-t";

/**
 * Fallback host for the LRM-619 human member profile panel. Channels/DM
 * clicks go through MemberPanelProvider (inline slot); everywhere else
 * this overlay opens from useMemberPanelStore — same pattern as
 * GlobalAgentPanel.
 */
export function GlobalMemberPanel() {
  const { t } = useT("members");
  const { pathname } = useNavigation();
  const selectedUserId = useMemberPanelStore((s) => s.selectedUserId);
  const identitySnapshot = useMemberPanelStore((s) => s.identitySnapshot);
  const close = useMemberPanelStore((s) => s.close);
  const closeAgent = useAgentPanelStore((s) => s.close);
  const { width, onResizePointerDown } = useProfilePanelWidth();

  useEffect(() => {
    close();
  }, [pathname, close]);

  // Mutual exclusion with the agent overlay — only one global profile dock.
  useEffect(() => {
    if (selectedUserId) closeAgent();
  }, [selectedUserId, closeAgent]);

  const [displayedId, setDisplayedId] = useState<string | null>(null);
  const [displayedSnapshot, setDisplayedSnapshot] =
    useState<MemberPanelIdentitySnapshot | null>(null);
  if (selectedUserId && selectedUserId !== displayedId) {
    setDisplayedId(selectedUserId);
    setDisplayedSnapshot(identitySnapshot);
  }

  const open = !!selectedUserId;
  const panelUserId = selectedUserId ?? displayedId;
  void displayedSnapshot;

  return (
    <Dialog.Root
      open={open}
      onOpenChange={(nextOpen) => {
        if (!nextOpen) close();
      }}
    >
      <Dialog.Portal>
        <Dialog.Backdrop className="fixed inset-0 z-40" />
        <Dialog.Popup
          className="fixed inset-y-0 right-0 z-50 max-w-[90vw] border-l border-border/30 bg-background shadow-2xl transition-transform duration-200 ease-in-out data-starting-style:translate-x-full data-ending-style:translate-x-full"
          style={{ width }}
          data-testid="global-member-panel"
        >
          <button
            type="button"
            data-testid="global-member-panel-resize"
            aria-label={t(($) => $.side_panel.resize_aria)}
            className="absolute inset-y-0 left-0 z-10 w-1.5 cursor-col-resize border-0 bg-transparent p-0 hover:bg-foreground/10 data-[separator=active]:bg-foreground/15"
            onPointerDown={onResizePointerDown}
          />
          {panelUserId ? (
            <HumanMemberSidePanel userId={panelUserId} onClose={close} />
          ) : null}
        </Dialog.Popup>
      </Dialog.Portal>
    </Dialog.Root>
  );
}
