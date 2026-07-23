"use client";

import { useRef, useState } from "react";
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
 * LRM-449 — sync ref lock so a double-click cannot fire two DELETE requests /
 * stacked failure toasts while the dialog stays open (AlertDialogAction is
 * not a Close). Reset the lock when `pending` flips false (parent re-render),
 * not via a prop→state effect (React Doctor).
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
  const lockedRef = useRef(false);
  const prevPendingRef = useRef(!!pending);

  // Clear the click lock when the mutation settles (pending true → false).
  if (prevPendingRef.current && !pending) {
    lockedRef.current = false;
  }
  prevPendingRef.current = !!pending;

  const busy = !!pending || lockedRef.current;

  return (
    <AlertDialog
      open={open}
      onOpenChange={(next) => {
        if (!next) {
          setConfirmed(false);
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
              if (!confirmed || lockedRef.current || pending) return;
              lockedRef.current = true;
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
