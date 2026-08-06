"use client";

import { useState } from "react";
import { Check, Crown, Shield, User } from "lucide-react";
import type { MemberRole } from "@multica/core/types";
import { Button } from "@multica/ui/components/ui/button";
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from "@multica/ui/components/ui/dialog";
import { cn } from "@multica/ui/lib/utils";
import { useT } from "../../i18n";

const ROLE_ICONS: Record<MemberRole, typeof Crown> = {
  owner: Crown,
  admin: Shield,
  member: User,
};

const ROLE_ORDER: MemberRole[] = ["owner", "admin", "member"];

/**
 * LRM-524 / LRM-469 lock A — workspace Roles dialog (not Agent Roles).
 * - info: Members-tab explanation (cards + close)
 * - select: change-role picker (selected state + Save)
 * Token chrome only — no neo-brutal borders/shadows.
 */
export function RolesDialog({
  open,
  onOpenChange,
  mode,
  value = "member",
  allowedRoles,
  disabledReasons,
  saving = false,
  onSave,
  title,
  subtitle,
}: {
  open: boolean;
  onOpenChange: (open: boolean) => void;
  mode: "info" | "select";
  value?: MemberRole;
  /** When set, only these roles are listed (e.g. hide owner for non-owners). */
  allowedRoles?: MemberRole[];
  /** Map of role → disabled reason (e.g. last-owner demotion). */
  disabledReasons?: Partial<Record<MemberRole, string>>;
  saving?: boolean;
  onSave?: (role: MemberRole) => void | Promise<void>;
  /** Optional overrides (e.g. agent workspace-role picker). */
  title?: string;
  subtitle?: string;
}) {
  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      {open ? (
        <RolesDialogBody
          key={`${mode}:${value}`}
          mode={mode}
          value={value}
          allowedRoles={allowedRoles}
          disabledReasons={disabledReasons}
          saving={saving}
          onOpenChange={onOpenChange}
          onSave={onSave}
          title={title}
          subtitle={subtitle}
        />
      ) : null}
    </Dialog>
  );
}

function RolesDialogBody({
  mode,
  value,
  allowedRoles,
  disabledReasons,
  saving,
  onOpenChange,
  onSave,
  title,
  subtitle,
}: {
  mode: "info" | "select";
  value: MemberRole;
  allowedRoles?: MemberRole[];
  disabledReasons?: Partial<Record<MemberRole, string>>;
  saving: boolean;
  onOpenChange: (open: boolean) => void;
  onSave?: (role: MemberRole) => void | Promise<void>;
  title?: string;
  subtitle?: string;
}) {
  const { t } = useT("settings");
  // Draft starts null (not copied from props). Parent remounts via `key` on
  // open/value change; selected = draft ?? value while rendering.
  const [draft, setDraft] = useState<MemberRole | null>(null);
  const selected = draft ?? value;

  const roles = ROLE_ORDER.filter((role) =>
    allowedRoles ? allowedRoles.includes(role) : true,
  );

  const dirty = selected !== value;
  const selectedDisabled = Boolean(disabledReasons?.[selected]);

  const handleSave = async () => {
    if (!onSave || !dirty || selectedDisabled || saving) return;
    await onSave(selected);
  };

  return (
    <DialogContent
      className={cn(
        "flex w-full flex-col gap-0 overflow-hidden border border-border bg-card p-0 sm:max-w-[440px]",
        "rounded-2xl shadow-lg",
        // Mobile: bottom sheet (same pattern as channel members dialog).
        "max-sm:top-auto max-sm:bottom-0 max-sm:left-1/2 max-sm:right-auto max-sm:max-w-full max-sm:w-full max-sm:translate-x-[-50%] max-sm:translate-y-0 max-sm:rounded-b-none max-sm:rounded-t-2xl",
      )}
      showCloseButton
    >
      <DialogHeader className="gap-1 px-5 pb-3 pt-5 text-left max-sm:px-2 sm:px-5">
        <DialogTitle className="text-base font-semibold">
          {title ?? t(($) => $.members.roles_dialog.title)}
        </DialogTitle>
        <DialogDescription className="text-xs text-muted-foreground">
          {subtitle ?? t(($) => $.members.roles_dialog.subtitle)}
        </DialogDescription>
      </DialogHeader>

      <div
        className="flex flex-col gap-2.5 px-5 pb-5 max-sm:px-2 sm:px-5"
        role={mode === "select" ? "radiogroup" : undefined}
        aria-label={t(($) => $.members.roles_dialog.title)}
      >
        {roles.map((role) => {
          const Icon = ROLE_ICONS[role];
          const disabledReason = disabledReasons?.[role];
          const disabled = Boolean(disabledReason);
          const isSelected = mode === "select" && selected === role;
          const label = t(($) => $.members.roles[role].label);
          const pill = t(($) => $.members.roles[role].pill);
          const description = t(($) => $.members.roles[role].description);

          const body = (
            <>
              <div className="flex h-8 w-8 shrink-0 items-center justify-center rounded-lg bg-brand/10 text-brand dark:bg-brand/15">
                <Icon className="h-[17px] w-[17px]" aria-hidden />
              </div>
              <div className="min-w-0 flex-1">
                <div className="flex flex-wrap items-center gap-2">
                  <span className="text-[13.5px] font-semibold leading-none">
                    {label}
                  </span>
                  <span className="rounded-full bg-muted px-1.5 py-0.5 text-[10.5px] font-semibold text-muted-foreground">
                    {pill}
                  </span>
                  {isSelected && !disabled && (
                    <Check
                      className="ml-auto h-4 w-4 shrink-0 text-brand"
                      aria-hidden
                    />
                  )}
                </div>
                <p className="mt-1 text-xs leading-relaxed text-muted-foreground">
                  {disabledReason ?? description}
                </p>
              </div>
            </>
          );

          if (mode === "info") {
            return (
              <div
                key={role}
                className="flex gap-3 rounded-xl border border-border bg-background p-3"
              >
                {body}
              </div>
            );
          }

          return (
            <button
              key={role}
              type="button"
              role="radio"
              aria-checked={isSelected}
              aria-disabled={disabled || undefined}
              disabled={disabled}
              onClick={() => {
                if (!disabled) setDraft(role);
              }}
              className={cn(
                "flex w-full gap-3 rounded-xl border border-border bg-background p-3 text-left transition-colors",
                "hover:bg-muted/40 focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-brand/40",
                isSelected && !disabled && "border-brand bg-brand/5",
                disabled && "cursor-not-allowed opacity-60 hover:bg-background",
              )}
            >
              {body}
            </button>
          );
        })}
      </div>

      {mode === "select" && (
        <DialogFooter className="border-border bg-muted/30 px-5 py-3 max-sm:px-2 sm:px-5">
          <Button
            type="button"
            variant="outline"
            disabled={saving}
            onClick={() => onOpenChange(false)}
          >
            {t(($) => $.members.roles_dialog.cancel)}
          </Button>
          <Button
            type="button"
            disabled={!dirty || selectedDisabled || saving}
            onClick={() => void handleSave()}
          >
            {saving
              ? t(($) => $.members.roles_dialog.saving)
              : t(($) => $.members.roles_dialog.save)}
          </Button>
        </DialogFooter>
      )}
    </DialogContent>
  );
}
