"use client";

import { useEffect, useState } from "react";
import { Dialog } from "@base-ui/react/dialog";
import { useMemberPanelStore } from "@multica/core/workspace";
import { useAgentPanelStore } from "@multica/core/agents/stores";
import { MemberSidePanel } from "../members/member-side-panel";
import { useNavigation } from "../navigation";
import { useProfilePanelWidth } from "./use-profile-panel-width";
import { useT } from "../i18n/use-t";

/**
 * LRM-619 — global fallback host for the human member Profile dock.
 * Mirrors GlobalAgentPanel; channels/DM use MemberPanelProvider instead.
 */
export function GlobalMemberPanel() {
  const { t } = useT("agents");
  const { pathname } = useNavigation();
  const selectedUserId = useMemberPanelStore((s) => s.selectedUserId);
  const close = useMemberPanelStore((s) => s.close);
  const closeAgent = useAgentPanelStore((s) => s.close);
  const { width, onResizePointerDown } = useProfilePanelWidth();

  useEffect(() => {
    close();
  }, [pathname, close]);

  // Opening a member panel dismisses any lingering global agent overlay.
  useEffect(() => {
    if (selectedUserId) closeAgent();
  }, [selectedUserId, closeAgent]);

  const [displayedId, setDisplayedId] = useState<string | null>(null);
  if (selectedUserId && selectedUserId !== displayedId) {
    setDisplayedId(selectedUserId);
  }

  const open = !!selectedUserId;
  const panelUserId = selectedUserId ?? displayedId;

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
          className="fixed inset-y-0 right-0 z-50 max-w-[90vw] border-l border-border/30 bg-background shadow-2xl transition-transform duration-200 ease-in-out motion-reduce:transition-none data-starting-style:translate-x-full data-ending-style:translate-x-full"
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
            <MemberSidePanel userId={panelUserId} onClose={close} />
          ) : null}
        </Dialog.Popup>
      </Dialog.Portal>
    </Dialog.Root>
  );
}
