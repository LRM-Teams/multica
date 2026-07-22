"use client";

import { useEffect, useState } from "react";
import {
  AlertDialog,
  AlertDialogCancel,
  AlertDialogContent,
  AlertDialogDescription,
  AlertDialogFooter,
  AlertDialogHeader,
  AlertDialogTitle,
} from "@multica/ui/components/ui/alert-dialog";
import { Button } from "@multica/ui/components/ui/button";
import { Checkbox } from "@multica/ui/components/ui/checkbox";
import { useT } from "../../i18n";

/**
 * LRM-237 — Slack-aligned permanent delete confirm: checkbox
 * 「是的，永久删除此频道」must be checked before Delete is enabled.
 * No channel-name typing (Frank/Beckham Slack 定稿).
 */
export function DeleteChannelDialog({
  open,
  channelName,
  pending,
  onOpenChange,
  onConfirm,
}: {
  open: boolean;
  channelName: string;
  pending?: boolean;
  onOpenChange: (open: boolean) => void;
  onConfirm: () => void;
}) {
  const { t } = useT("channels");
  const [confirmed, setConfirmed] = useState(false);

  useEffect(() => {
    if (!open) setConfirmed(false);
  }, [open]);

  return (
    <AlertDialog open={open} onOpenChange={onOpenChange}>
      <AlertDialogContent>
        <AlertDialogHeader>
          <AlertDialogTitle>{t(($) => $.delete_dialog.title)}</AlertDialogTitle>
          <AlertDialogDescription>
            {t(($) => $.delete_dialog.description, { name: channelName })}
          </AlertDialogDescription>
        </AlertDialogHeader>

        <label className="flex cursor-pointer items-start gap-2 rounded-lg bg-muted/40 px-2.5 py-2.5 text-sm">
          <Checkbox
            className="mt-0.5"
            checked={confirmed}
            onCheckedChange={(next) => setConfirmed(next === true)}
            disabled={pending}
          />
          <span className="leading-5">{t(($) => $.delete_dialog.checkbox)}</span>
        </label>

        <AlertDialogFooter>
          <AlertDialogCancel disabled={pending}>
            {t(($) => $.delete_dialog.cancel)}
          </AlertDialogCancel>
          <Button
            type="button"
            variant="destructive"
            disabled={!confirmed || pending}
            onClick={onConfirm}
          >
            {t(($) => $.delete_dialog.confirm)}
          </Button>
        </AlertDialogFooter>
      </AlertDialogContent>
    </AlertDialog>
  );
}
