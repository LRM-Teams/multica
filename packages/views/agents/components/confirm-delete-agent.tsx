"use client";

import { useRef, type ComponentProps } from "react";
import { AlertCircle } from "lucide-react";
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
import { useT } from "../../i18n/use-t";

/**
 * LRM-865 — Shared second-confirm gate for every "Delete agent" entry.
 * Soft-delete via archive under the hood (history preserved / restorable);
 * user-facing copy stays "Delete" per LRM-448. Keeps the dialog open while
 * `pending`, disables both buttons, and shows "Deleting…". Esc / overlay /
 * Cancel dismiss only when not pending.
 */
export function ConfirmDeleteAgent({
  open,
  displayName,
  pending,
  onConfirm,
  onOpenChange,
  contentProps,
}: {
  open: boolean;
  displayName: string;
  pending?: boolean;
  onConfirm: () => void;
  onOpenChange: (open: boolean) => void;
  /** Extra props for AlertDialogContent (e.g. stopPropagation on list rows). */
  contentProps?: ComponentProps<typeof AlertDialogContent>;
}) {
  const { t } = useT("agents");
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
        if (!next && busy) return;
        if (!next) lockedRef.current = false;
        onOpenChange(next);
      }}
    >
      <AlertDialogContent
        data-testid="confirm-delete-agent"
        {...contentProps}
      >
        <AlertDialogHeader>
          <div className="flex items-start gap-3">
            <div className="flex h-10 w-10 shrink-0 items-center justify-center rounded-full bg-destructive/10">
              <AlertCircle className="h-5 w-5 text-destructive" aria-hidden />
            </div>
            <div className="flex-1 text-left">
              <AlertDialogTitle>
                {t(($) => $.delete_confirm.title)}
              </AlertDialogTitle>
              <AlertDialogDescription>
                {t(($) => $.delete_confirm.description, { name: displayName })}
              </AlertDialogDescription>
            </div>
          </div>
        </AlertDialogHeader>
        <AlertDialogFooter>
          <AlertDialogCancel disabled={busy} autoFocus>
            {t(($) => $.delete_confirm.cancel)}
          </AlertDialogCancel>
          <AlertDialogAction
            variant="destructive"
            disabled={busy}
            data-testid="confirm-delete-agent-confirm"
            onClick={() => {
              if (lockedRef.current || pending) return;
              lockedRef.current = true;
              onConfirm();
            }}
          >
            {busy
              ? t(($) => $.delete_confirm.confirming)
              : t(($) => $.delete_confirm.confirm)}
          </AlertDialogAction>
        </AlertDialogFooter>
      </AlertDialogContent>
    </AlertDialog>
  );
}
