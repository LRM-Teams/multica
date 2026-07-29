import { useState } from "react";
import { useQuery } from "@tanstack/react-query";
import { X } from "lucide-react";
import { channelMembersOptions, channelMemberRole } from "@multica/core/channels";
import { useAuthStore } from "@multica/core/auth";
import { Button } from "@multica/ui/components/ui/button";
import { useT } from "../../i18n";
import { readGroupManagerHintDismissed, dismissGroupManagerHint } from "./group-manager-hint-dismissal";

/**
 * #808 slice 2 — onboarding hint for a group OWNER whose group has no group
 * manager yet (Beckham v2 §4; copy locked by Iris).
 *
 * Deliberately narrow, and fail-closed on every axis:
 *   - Renders ONLY for the viewer who is the channel `owner`, and ONLY while the
 *     roster has zero `manager` members. Any other viewer sees nothing.
 *   - `channelMemberRole` defaults a missing role to "member", so if the server
 *     doesn't (yet) return `channel_member.role`, the owner check simply fails
 *     and the hint stays hidden — we never guess "this group has no manager"
 *     from absent data, and never imply a permission we can't verify.
 *   - It never assigns anything: the CTA only navigates to the member list.
 *     That is deliberate, not a limitation — designating a manager is done in
 *     the member list where the target and the consequence are both visible,
 *     never inline from a hint (no auto-provisioned agent manager — Frank/
 *     Parker locked that).
 *   - Dismissible per channel, remembered locally.
 */
export function GroupManagerHint({
  channelId,
  onOpenMembers,
}: {
  channelId: string;
  /** Navigate to the member list (Details → Members). No assignment happens. */
  onOpenMembers: () => void;
}) {
  const { t } = useT("channels");
  // Self-contained: reads the viewer and the roster itself so the host surface
  // only has to say "render here" (no prop threading through the panel).
  const currentUserId = useAuthStore((s) => s.user?.id ?? null);
  const { data: members } = useQuery(channelMembersOptions(channelId));
  const [dismissed, setDismissed] = useState(() =>
    readGroupManagerHintDismissed(channelId),
  );

  if (dismissed || !members) return null;

  // Roster role data must be COMPLETE before we can claim "there is no manager".
  // Read the raw field: `channelMemberRole` collapses a missing role to
  // "member", which is exactly the distinction that matters here — a real
  // manager whose role the server omitted would otherwise be counted as an
  // ordinary member and the hint would announce "no manager yet" to a group
  // that has one. A roster that omits `role` for some members is a shape the
  // server can still return, so this guard is load-bearing, not theoretical.
  // (Iris/Wren review catch.)
  if (members.some((m) => m.role === undefined)) return null;

  const viewer = members.find(
    (m) => m.member_type === "user" && m.member_id === currentUserId,
  );
  // Owner-only. Absent/unknown role → "member" → hidden (fail closed).
  if (!viewer || channelMemberRole(viewer) !== "owner") return null;
  // Only while the group genuinely has no manager.
  if (members.some((m) => channelMemberRole(m) === "manager")) return null;

  return (
    <div
      className="mx-3 mb-2 mt-3 rounded-lg border border-border bg-muted/40 p-3"
      data-testid="group-manager-hint"
    >
      <div className="flex items-start gap-2">
        <div className="min-w-0 flex-1">
          <p className="text-sm font-semibold">
            {t(($) => $.group_manager_hint.title)}
          </p>
          <p className="mt-1 text-xs leading-relaxed text-muted-foreground">
            {t(($) => $.group_manager_hint.body)}
          </p>
        </div>
        <button
          type="button"
          onClick={() => {
            dismissGroupManagerHint(channelId);
            setDismissed(true);
          }}
          aria-label={t(($) => $.group_manager_hint.dismiss)}
          className="shrink-0 rounded-md p-1 text-muted-foreground hover:bg-background hover:text-foreground"
          data-testid="group-manager-hint-dismiss"
        >
          <X className="size-4" />
        </button>
      </div>
      <div className="mt-2.5 flex items-center gap-2">
        <Button
          type="button"
          size="sm"
          onClick={onOpenMembers}
          data-testid="group-manager-hint-cta"
        >
          {t(($) => $.group_manager_hint.cta)}
        </Button>
        <Button
          type="button"
          size="sm"
          variant="ghost"
          onClick={() => {
            dismissGroupManagerHint(channelId);
            setDismissed(true);
          }}
        >
          {t(($) => $.group_manager_hint.dismiss)}
        </Button>
      </div>
    </div>
  );
}
