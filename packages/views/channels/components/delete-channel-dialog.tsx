"use client";

import { useEffect, useRef, useState } from "react";
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
 * LRM-449 — sync lock + pending disable so a double-click cannot fire two
 * DELETE requests / stacked failure toasts while the dialog stays open
 * (AlertDialogAction is not a Close).
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
  const [submitting, setSubmitting] = useState(false);
  const lockedRef = useRef(false);
  const busy = !!pending || submitting;

  useEffect(() => {
    if (!pending) {
      lockedRef.current = false;
      setSubmitting(false);
    }
  }, [pending]);

  return (
    <AlertDialog
      open={open}
      onOpenChange={(next) => {
        if (!next) {
          setConfirmed(false);
          setSubmitting(false);
          lockedRef.current = false;
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
          <AlertDialogCancel disabled={busy}>{t(($) => $.delete_dialog.cancel)}</AlertDialogCancel>
          <AlertDialogAction
            variant="destructive"
            onClick={() => {
              if (!confirmed || lockedRef.current || busy) return;
              lockedRef.current = true;
              setSubmitting(true);
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
