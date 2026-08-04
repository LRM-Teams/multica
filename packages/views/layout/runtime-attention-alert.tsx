"use client";

import type { ReactElement } from "react";
import { TriangleAlert } from "lucide-react";
import { useMyAttentionRuntimeSummary } from "@multica/core/runtimes/hooks";
import {
  Popover,
  PopoverContent,
  PopoverTrigger,
} from "@multica/ui/components/ui/popover";
import { useWorkspacePaths } from "@multica/core/paths";
import { AppLink } from "../navigation/app-link";
import { useT } from "../i18n";

type RuntimeAttentionAlertProps = {
  wsId: string | undefined;
  trigger?: ReactElement;
};

/**
 * Task #9 (2026-07-31, Frank DM): replaces the modal "your daemon needs an
 * upgrade" popup, which grabbed focus on every visit for a non-urgent,
 * routine maintenance fact. This is a small warning-tone icon next to the
 * "Computers" nav item (same slot the old red dot used) that opens a
 * non-modal popover on click/hover — click outside to dismiss, never steals
 * focus.
 *
 * Deliberately warning/amber, never destructive/red (Iris, approved by
 * Parker): "needs an upgrade" is routine maintenance, not a failure — the
 * same red-for-a-normal-notice mismatch that startled Frank earlier today
 * elsewhere in the product.
 *
 * The popover's ONLY job is "tell you + take you there" — it does not embed
 * an upgrade button. The actual upgrade action lives on the runtime detail
 * page (task #8); duplicating it here would mean two surfaces owning one
 * piece of state, the same class of problem as the machine-name pencil
 * icons appearing in three places at once.
 *
 * Renders null when nothing needs attention — no icon, no dot, nothing.
 * Sandbox daemons never count here: `useMyAttentionRuntimeCount` shares the
 * same `runtimeHasHealthAttention` predicate as the icon it replaces, which
 * already excludes them (isSandboxRuntime, #1643) — a sandbox user can't
 * self-update, so the icon must never light up for them.
 */
export function RuntimeAttentionAlert({
  wsId,
  trigger,
}: RuntimeAttentionAlertProps) {
  const { t } = useT("layout");
  const paths = useWorkspacePaths();
  const { count, firstRuntimeId } = useMyAttentionRuntimeSummary(wsId);

  if (count === 0 || !firstRuntimeId) return null;

  return (
    <Popover>
      <PopoverTrigger
        render={
          trigger ?? (
            <button
              type="button"
              className="inline-flex items-center justify-center rounded-sm outline-none focus-visible:ring-1 focus-visible:ring-ring"
            />
          )
        }
        aria-label={t(($) => $.runtime_attention.trigger_label, { count })}
      >
        <TriangleAlert className="size-3.5 text-warning" />
      </PopoverTrigger>
      <PopoverContent align="start" className="w-64">
        <p className="text-xs text-foreground">
          {t(($) => $.runtime_attention.count, { count })}
        </p>
        <AppLink
          href={paths.computersAttention(firstRuntimeId)}
          className="text-xs font-medium text-brand hover:underline"
        >
          {t(($) => $.runtime_attention.view)}
        </AppLink>
      </PopoverContent>
    </Popover>
  );
}
