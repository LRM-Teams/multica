"use client";

import { useMemo } from "react";
import { useQueryClient } from "@tanstack/react-query";
import { toast } from "sonner";
import type { HonorBadgeUnlockedPayload } from "@multica/core/types/events";
import { getCurrentWsId } from "@multica/core/platform";
import { useWSEvent } from "@multica/core/realtime";
import { workspaceKeys } from "@multica/core/workspace/queries";
import { HonorBadgeCrest } from "@multica/ui/components/honor/honor-badge";
import { Gem, Sparkles, X } from "lucide-react";
import { useT } from "../i18n";

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
        <output
          className="relative w-[min(420px,calc(100vw-2rem))] overflow-hidden rounded-2xl border border-cyan-300/20 bg-slate-950 p-4 text-white shadow-[0_24px_80px_-28px_rgba(34,211,238,0.8)]"
        >
          <div
            aria-hidden="true"
            className="absolute inset-0 bg-[radial-gradient(circle_at_0%_0%,rgba(34,211,238,0.18),transparent_45%),radial-gradient(circle_at_100%_100%,rgba(139,92,246,0.22),transparent_48%)]"
          />
          <div
            aria-hidden="true"
            className="absolute inset-x-0 top-0 h-px bg-gradient-to-r from-transparent via-cyan-200/80 to-transparent"
          />
          <button
            type="button"
            aria-label={t(($) => $.honor.dismiss_unlock)}
            onClick={() => toast.dismiss(toastId)}
            className="absolute right-3 top-3 z-10 grid size-7 place-items-center rounded-full text-slate-400 outline-none transition-colors hover:bg-white/10 hover:text-white focus-visible:ring-2 focus-visible:ring-cyan-300"
          >
            <X className="size-3.5" aria-hidden="true" />
          </button>
          <div className="relative flex items-center gap-4 pr-7">
            <div className="motion-safe:animate-[honor-unlock-in_700ms_cubic-bezier(0.16,1,0.3,1)] motion-reduce:animate-none">
              <HonorBadgeCrest
                svgKey={event.badge.svg_key}
                title={event.badge.title}
                rare={rare}
                className="size-[4.5rem]"
              />
            </div>
            <div className="min-w-0 flex-1">
              <p className="flex items-center gap-1.5 font-mono text-[10px] uppercase tracking-[0.16em] text-cyan-300">
                {rare ? (
                  <Gem className="size-3" aria-hidden="true" />
                ) : (
                  <Sparkles className="size-3" aria-hidden="true" />
                )}
                {rare
                  ? t(($) => $.honor.unlock_toast_rare_title)
                  : t(($) => $.honor.unlock_toast_title)}
              </p>
              <p className="mt-1 truncate text-base font-semibold">
                {event.badge.title}
              </p>
              <p className="mt-1 text-xs leading-5 text-slate-400">
                {t(($) => $.honor.unlock_toast_description, {
                  title: event.badge.title,
                })}
              </p>
              {event.unlock_pct != null && event.unlock_pct > 0 ? (
                <p className="mt-1.5 font-mono text-[10px] text-amber-300">
                  {t(($) => $.honor.rarity_pct, {
                    pct: percentFormatter.format(event.unlock_pct),
                  })}
                </p>
              ) : null}
            </div>
          </div>
        </output>
      ),
      { duration: 8000 },
    );
  });

  return null;
}
