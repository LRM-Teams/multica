"use client";

import { useRef } from "react";
import { Loader2Icon } from "lucide-react";
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
 * LRM-1327 — Channel "remove member" confirm (LRM-1300 A / D3).
 * AlertDialog only: pending shows spinner + 「移除中…」, blocks Esc/overlay,
 * initial focus on Cancel. D1/D2 wash tokens stay on LRM-1328.
 */
export function RemoveMemberConfirmDialog({
  open,
  displayName,
  pending,
  onConfirm,
  onOpenChange,
}: {
  open: boolean;
  displayName: string;
  pending?: boolean;
  onConfirm: () => void;
  onOpenChange: (open: boolean) => void;
}) {
  const { t } = useT("channels");
  // Sync double-click lock. Must clear when:
  // - pending true→false (normal RQ path), OR
  // - we submitted but never observed pending (sync mock reject / same-tick settle)
  //   — otherwise the button sticks on「移除中…」and #839 row notice stays aria-hidden.
  const lockedRef = useRef(false);
  const submittedRef = useRef(false);
  const prevPendingRef = useRef(!!pending);

  if (pending) {
    prevPendingRef.current = true;
  } else {
    if (prevPendingRef.current || submittedRef.current) {
      lockedRef.current = false;
      submittedRef.current = false;
    }
    prevPendingRef.current = false;
  }

  const busy = !!pending || lockedRef.current;

  return (
    <AlertDialog
      open={open}
      onOpenChange={(next) => {
        // §5 提交中：Esc / 遮罩不关
        if (!next && busy) return;
        if (!next) {
          lockedRef.current = false;
          submittedRef.current = false;
        }
        onOpenChange(next);
      }}
    >
      <AlertDialogContent data-testid="group-member-remove-confirm">
        <AlertDialogHeader>
          <AlertDialogTitle className="line-clamp-2 break-words [overflow-wrap:anywhere]">
            {t(($) => $.members.remove_confirm_title, { name: displayName })}
          </AlertDialogTitle>
          <AlertDialogDescription>
            {t(($) => $.members.remove_confirm_description)}
          </AlertDialogDescription>
        </AlertDialogHeader>
        <AlertDialogFooter>
          <AlertDialogCancel
            disabled={busy}
            autoFocus
            className="min-h-11 w-full sm:min-h-8 sm:w-auto"
            data-testid="group-member-remove-cancel"
          >
            {t(($) => $.members.remove_cancel)}
          </AlertDialogCancel>
          <AlertDialogAction
            variant="destructive"
            disabled={busy}
            className="min-h-11 w-full sm:min-h-8 sm:w-auto"
            data-testid="group-member-remove-confirm-action"
            onClick={() => {
              if (lockedRef.current || pending) return;
              lockedRef.current = true;
              submittedRef.current = true;
              onConfirm();
            }}
          >
            {busy ? (
              <>
                <Loader2Icon
                  className="size-4 animate-spin motion-reduce:hidden"
                  aria-hidden
                />
                {t(($) => $.members.remove_confirming)}
              </>
            ) : (
              t(($) => $.members.remove_confirm)
            )}
          </AlertDialogAction>
        </AlertDialogFooter>
      </AlertDialogContent>
    </AlertDialog>
  );
}
