"use client";

import { useEffect, useState } from "react";
import { Check, Crown, Shield, User, X } from "lucide-react";
import type { MemberRole } from "@multica/core/types";
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from "@multica/ui/components/ui/dialog";
import { Button } from "@multica/ui/components/ui/button";
import { cn } from "@multica/ui/lib/utils";
import { useT } from "../../i18n";

const ROLE_ICONS: Record<MemberRole, typeof Crown> = {
  owner: Crown,
  admin: Shield,
  member: User,
};

const ALL_ROLES: MemberRole[] = ["owner", "admin", "member"];

/**
 * LRM-524 / LRM-469 lock A — workspace Roles dialog (not Agent Roles).
 * - `info`: read-only Owner/Admin/Member cards (Members → Roles entry)
 * - `select`: icon cards + selected state + Save (role picker)
 * Token chrome only (1px border, ghost close) — no neo-brutal.
 */
export function RolesDialog({
  open,
  onOpenChange,
  mode = "info",
  value = "member",
  roles = ALL_ROLES,
  saving = false,
  onSave,
}: {
  open: boolean;
  onOpenChange: (open: boolean) => void;
  mode?: "info" | "select";
  value?: MemberRole;
  /** Roles shown as options (invite picker omits owner). */
  roles?: MemberRole[];
  saving?: boolean;
  onSave?: (role: MemberRole) => void;
}) {
  const { t } = useT("settings");
  const [draft, setDraft] = useState<MemberRole>(value);

  useEffect(() => {
    if (open) setDraft(value);
  }, [open, value]);

  const selectable = mode === "select";

  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent
        className="relative gap-0 overflow-hidden rounded-2xl border border-border bg-card p-0 sm:max-w-[440px]"
        showCloseButton={false}
        data-testid="roles-dialog"
      >
        <DialogHeader className="space-y-1 border-b border-border px-5 py-4 pr-12 text-left">
          <div className="flex items-start justify-between gap-3">
            <div className="min-w-0">
              <DialogTitle className="text-base font-semibold">
                {t(($) => $.members.roles_dialog.title)}
              </DialogTitle>
              <DialogDescription className="mt-1 text-xs text-muted-foreground">
                {t(($) => $.members.roles_dialog.description)}
              </DialogDescription>
            </div>
            <Button
              type="button"
              variant="ghost"
              size="icon-sm"
              className="absolute right-3 top-3 size-7 text-muted-foreground hover:bg-muted"
              aria-label={t(($) => $.members.roles_dialog.close)}
              onClick={() => onOpenChange(false)}
            >
              <X className="size-4" />
            </Button>
          </div>
        </DialogHeader>

        <div className="flex flex-col gap-2.5 px-5 py-4" role="list">
          {roles.map((role) => {
            const Icon = ROLE_ICONS[role];
            const selected = selectable && draft === role;
            return (
              <button
                key={role}
                type="button"
                role="listitem"
                disabled={!selectable}
                aria-pressed={selectable ? selected : undefined}
                data-testid={`roles-dialog-card-${role}`}
                onClick={() => {
                  if (selectable) setDraft(role);
                }}
                className={cn(
                  "flex w-full items-start gap-3 rounded-xl border border-border bg-background p-3 text-left transition-colors",
                  selectable && "hover:bg-muted/40",
                  selected && "border-brand/40 bg-brand/[0.06] ring-1 ring-brand/20",
                  !selectable && "cursor-default",
                )}
              >
                <span className="flex size-8 shrink-0 items-center justify-center rounded-lg bg-brand/10 text-brand dark:bg-brand/[0.14]">
                  <Icon className="size-4" aria-hidden />
                </span>
                <span className="min-w-0 flex-1">
                  <span className="flex flex-wrap items-center gap-2">
                    <span className="text-sm font-semibold">
                      {t(($) => $.members.roles[role].label)}
                    </span>
                    <span className="rounded-md bg-muted px-1.5 py-0.5 text-[10.5px] font-semibold text-muted-foreground">
                      {t(($) => $.members.roles[role].badge)}
                    </span>
                    {selected ? (
                      <Check
                        className="ml-auto size-4 shrink-0 text-brand"
                        aria-hidden
                      />
                    ) : null}
                  </span>
                  <span className="mt-1 block text-xs leading-relaxed text-muted-foreground">
                    {t(($) => $.members.roles[role].description)}
                  </span>
                </span>
              </button>
            );
          })}
        </div>

        {selectable ? (
          <DialogFooter className="gap-2 border-t border-border px-5 py-3 sm:justify-end">
            <Button
              type="button"
              variant="ghost"
              disabled={saving}
              onClick={() => onOpenChange(false)}
            >
              {t(($) => $.members.roles_dialog.cancel)}
            </Button>
            <Button
              type="button"
              disabled={saving || draft === value}
              data-testid="roles-dialog-save"
              onClick={() => onSave?.(draft)}
            >
              {saving
                ? t(($) => $.members.roles_dialog.saving)
                : t(($) => $.members.roles_dialog.save)}
            </Button>
          </DialogFooter>
        ) : null}
      </DialogContent>
    </Dialog>
  );
}
