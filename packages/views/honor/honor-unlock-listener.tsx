"use client";

import { useMemo } from "react";
import { useQueryClient } from "@tanstack/react-query";
import { toast } from "sonner";
import type { HonorBadgeUnlockedPayload } from "@multica/core/types/events";
import { getCurrentWsId } from "@multica/core/platform";
import { useWSEvent } from "@multica/core/realtime";
import { workspaceKeys } from "@multica/core/workspace/queries";
import { useT } from "../i18n";
import {
  HonorUnlockToast,
  honorUnlockToastOptions,
} from "./honor-unlock-toast";

const rareUnlockPercentage = 9;

/** Listens for honor:badge_unlocked and presents a localized unlock ceremony. */
export function HonorUnlockListener() {
  const qc = useQueryClient();
  const { t, i18n } = useT("settings");
  const percentFormatter = useMemo(
    () =>
      new Intl.NumberFormat(i18n.resolvedLanguage || i18n.language, {
        maximumFractionDigits: 1,
      }),
    [i18n.language, i18n.resolvedLanguage],
  );

  useWSEvent("honor:badge_unlocked", (payload: unknown) => {
    const event = payload as HonorBadgeUnlockedPayload;
    qc.invalidateQueries({ queryKey: ["honor", "me"] });
    const wsId = getCurrentWsId();
    if (wsId) {
      qc.invalidateQueries({ queryKey: workspaceKeys.members(wsId) });
    }

    const rare =
      event.unlock_pct != null &&
      event.unlock_pct > 0 &&
      event.unlock_pct <= rareUnlockPercentage;
    toast.custom(
      (toastId) => (
        <HonorUnlockToast
          eyebrow={
            rare
              ? t(($) => $.honor.unlock_toast_rare_title)
              : t(($) => $.honor.unlock_toast_title)
          }
          title={event.badge.title}
          meta={
            event.unlock_pct != null && event.unlock_pct > 0
              ? t(($) => $.honor.rarity_pct, {
                  pct: percentFormatter.format(event.unlock_pct),
                })
              : t(($) => $.honor.unlock_toast_description, {
                  title: event.badge.title,
                })
          }
          svgKey={event.badge.svg_key}
          rare={rare}
          dismissLabel={t(($) => $.honor.dismiss_unlock)}
          onDismiss={() => toast.dismiss(toastId)}
        />
      ),
      honorUnlockToastOptions,
    );
  });

  return null;
}
