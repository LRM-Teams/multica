"use client";

import { useState } from "react";
import type { MemberRole, MemberWithUser } from "@multica/core/types";
import { Button } from "@multica/ui/components/ui/button";
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "@multica/ui/components/ui/select";
import {
  AlertDialog,
  AlertDialogAction,
  AlertDialogCancel,
  AlertDialogContent,
  AlertDialogDescription,
  AlertDialogFooter,
  AlertDialogHeader,
  AlertDialogTitle,
} from "@multica/ui/components/ui/alert-dialog";
import { resolveActorDisplayName } from "@multica/core/identity";
import { useT } from "../i18n";

export function MemberDirectoryManageFooter({
  member,
  currentUserId,
  isOwner,
  ownerCount,
  busy,
  onRoleChange,
  onRemove,
}: {
  member: MemberWithUser;
  currentUserId: string | null;
  isOwner: boolean;
  ownerCount: number;
  busy: boolean;
  onRoleChange: (role: MemberRole) => Promise<void>;
  onRemove: () => Promise<void>;
}) {
  const { t } = useT("members");
  const { t: tSettings } = useT("settings");
  const [confirmRemove, setConfirmRemove] = useState(false);
  const isSelf = currentUserId === member.user_id;
  const isLastOwner = member.role === "owner" && ownerCount <= 1;

  const roleOptions: MemberRole[] = isOwner
    ? ["owner", "admin", "member"]
    : ["admin", "member"];

  const canEditRole =
    !isSelf &&
    !(member.role === "owner" && !isOwner) &&
    !isLastOwner;

  const canRemove =
    !isSelf &&
    !(member.role === "owner" && !isOwner) &&
    !isLastOwner;

  return (
    <div
      className="shrink-0 space-y-3 border-t bg-background px-4 py-3"
      data-testid="member-directory-manage"
    >
      <div className="flex items-center gap-3">
        <span className="w-14 text-xs text-muted-foreground">
          {t(($) => $.panel.role)}
        </span>
        {canEditRole ? (
          <Select
            value={member.role}
            onValueChange={(v) => void onRoleChange(v as MemberRole)}
            disabled={busy}
          >
            <SelectTrigger
              size="sm"
              className="w-36"
              data-testid="member-directory-role-select"
            >
              <SelectValue />
            </SelectTrigger>
            <SelectContent>
              {roleOptions.map((r) => (
                <SelectItem key={r} value={r}>
                  {tSettings(($) => $.members.roles[r].label)}
                </SelectItem>
              ))}
            </SelectContent>
          </Select>
        ) : (
          <span className="text-sm">{t(($) => $.role[member.role])}</span>
        )}
      </div>
      {canRemove ? (
        <Button
          type="button"
          variant="destructive"
          className="w-full"
          disabled={busy}
          onClick={() => setConfirmRemove(true)}
          data-testid="member-directory-remove"
        >
          {t(($) => $.directory.remove_member)}
        </Button>
      ) : null}

      <AlertDialog open={confirmRemove} onOpenChange={setConfirmRemove}>
        <AlertDialogContent>
          <AlertDialogHeader>
            <AlertDialogTitle>
              {t(($) => $.directory.remove_confirm_title, {
                name: resolveActorDisplayName(member, member.user_id),
              })}
            </AlertDialogTitle>
            <AlertDialogDescription>
              {t(($) => $.directory.remove_confirm_description)}
            </AlertDialogDescription>
          </AlertDialogHeader>
          <AlertDialogFooter>
            <AlertDialogCancel>
              {t(($) => $.directory.cancel)}
            </AlertDialogCancel>
            <AlertDialogAction
              className="bg-destructive text-destructive-foreground hover:bg-destructive/90"
              onClick={() => {
                void onRemove().finally(() => setConfirmRemove(false));
              }}
            >
              {t(($) => $.directory.remove_member)}
            </AlertDialogAction>
          </AlertDialogFooter>
        </AlertDialogContent>
      </AlertDialog>
    </div>
  );
}
