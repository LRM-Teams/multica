"use client";

import { useEffect, useState } from "react";
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
import { Checkbox } from "@multica/ui/components/ui/checkbox";
import { useT } from "../../i18n";

/**
 * LRM-239 — Slack-aligned permanent delete confirm: the destructive action
 * stays disabled until the caller checks "Yes, permanently delete…".
 */
export function DeleteChannelDialog({
  open,
  channelName,
  pending,
  onConfirm,
  onOpenChange,
}: {
  open: boolean;
  channelName: string;
  pending?: boolean;
  onConfirm: () => void;
  onOpenChange: (open: boolean) => void;
}) {
  const { t } = useT("channels");
  const [confirmed, setConfirmed] = useState(false);
  // LRM-449 — AlertDialogAction is a plain Button (does not auto-close). Local
  // submitted flag disables the action before React Query flips `pending`.
  const [submitted, setSubmitted] = useState(false);

  useEffect(() => {
    if (!open) {
      setConfirmed(false);
      setSubmitted(false);
    }
  }, [open]);

  useEffect(() => {
    if (!pending) setSubmitted(false);
  }, [pending]);

  const busy = !!pending || submitted;

  return (
    <AlertDialog
      open={open}
      onOpenChange={(next) => {
        if (!next) {
          setConfirmed(false);
          setSubmitted(false);
        }
        onOpenChange(next);
      }}
    >
      <AlertDialogContent>
        <AlertDialogHeader>
          <AlertDialogTitle>{t(($) => $.delete_dialog.title)}</AlertDialogTitle>
          <AlertDialogDescription>
            {t(($) => $.delete_dialog.description, { name: channelName })}
          </AlertDialogDescription>
        </AlertDialogHeader>
        <label className="flex cursor-pointer items-start gap-2 rounded-lg bg-muted/40 px-3 py-2.5 text-sm text-foreground">
          <Checkbox
            className="mt-0.5"
            checked={confirmed}
            onCheckedChange={(next) => setConfirmed(next === true)}
            disabled={busy}
          />
          <span className="leading-5">{t(($) => $.delete_dialog.confirm_checkbox)}</span>
        </label>
        <AlertDialogFooter>
          <AlertDialogCancel disabled={!!pending}>{t(($) => $.delete_dialog.cancel)}</AlertDialogCancel>
          <AlertDialogAction
            variant="destructive"
            onClick={() => {
              if (!confirmed || busy) return;
              setSubmitted(true);
              onConfirm();
            }}
            disabled={!confirmed || busy}
          >
            {t(($) => $.delete_dialog.confirm)}
          </AlertDialogAction>
        </AlertDialogFooter>
      </AlertDialogContent>
    </AlertDialog>
  );
}
