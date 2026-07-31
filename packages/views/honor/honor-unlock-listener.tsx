"use client";

import { useQueryClient } from "@tanstack/react-query";
import { toast } from "sonner";
import type { HonorBadgeUnlockedPayload } from "@multica/core/types/events";
import { getCurrentWsId } from "@multica/core/platform";
import { useWSEvent } from "@multica/core/realtime";
import { workspaceKeys } from "@multica/core/workspace/queries";
import { HonorBadgeIcon } from "@multica/ui/components/honor/honor-badge";

/** Listens for honor:badge_unlocked and shows an Xbox-style unlock toast. */
export function HonorUnlockListener() {
  const qc = useQueryClient();

  useWSEvent("honor:badge_unlocked", (payload: unknown) => {
    const event = payload as HonorBadgeUnlockedPayload;
    qc.invalidateQueries({ queryKey: ["honor", "me"] });
    const wsId = getCurrentWsId();
    if (wsId) {
      qc.invalidateQueries({ queryKey: workspaceKeys.members(wsId) });
    }
    const rarity =
      event.unlock_pct != null && event.unlock_pct > 0
        ? ` · ${event.unlock_pct.toFixed(1)}% of users`
        : "";
    toast.success(`Achievement unlocked: ${event.badge.title}${rarity}`, {
      icon: (
        <HonorBadgeIcon svgKey={event.badge.svg_key} title={event.badge.title} medal />
      ),
      duration: 6000,
    });
  });

  return null;
}
