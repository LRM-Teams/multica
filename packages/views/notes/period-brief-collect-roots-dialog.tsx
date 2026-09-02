"use client";

import { useEffect, useRef, useState } from "react";
import { ApiError, api } from "@multica/core/api";
import { Button } from "@multica/ui/components/ui/button";
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogHeader,
  DialogTitle,
} from "@multica/ui/components/ui/dialog";
import { showErrorToast } from "@multica/ui/lib/error-toast";
import { toast } from "sonner";
import { useT } from "../i18n/use-t";

function isCollectRootsTimeout(error: unknown): boolean {
  if (!(error instanceof ApiError)) return false;
  const body = error.body;
  return Boolean(
    body &&
      typeof body === "object" &&
      "code" in body &&
      body.code === "computer_collect_roots_timeout",
  );
}

function collectRootsErrorMessage(error: unknown, timeoutCopy: string): string {
  if (isCollectRootsTimeout(error)) return timeoutCopy;
  return error instanceof Error ? error.message : String(error);
}

export function PeriodBriefCollectRootsDialog({
  machineId,
  label,
  online,
  onClose,
}: {
  machineId: string;
  label: string;
  online: boolean;
  onClose: () => void;
}) {
  const { t } = useT("layout");
  const [text, setText] = useState("");
  const [saving, setSaving] = useState(false);
  const [notice, setNotice] = useState<"offline" | "timeout" | null>(
    online ? null : "offline",
  );
  const dirtyRef = useRef(false);

  useEffect(() => {
    setNotice(online ? null : "offline");
  }, [online]);

  useEffect(() => {
    let cancelled = false;
    dirtyRef.current = false;
    setText("");
    void api
      .getComputerCollectRoots(machineId)
      .then((result) => {
        if (cancelled || dirtyRef.current) return;
        setText(result.roots.join("\n"));
        setNotice(null);
      })
      .catch((error: unknown) => {
        if (cancelled) return;
        if (isCollectRootsTimeout(error)) {
          setNotice("timeout");
          return;
        }
        setNotice("offline");
        showErrorToast(error instanceof Error ? error.message : String(error));
      });
    return () => {
      cancelled = true;
    };
  }, [machineId]);

  const save = async () => {
    const roots = text
      .split("\n")
      .map((line) => line.trim())
      .filter(Boolean);
    setSaving(true);
    try {
      const result = await api.patchComputerCollectRoots(machineId, roots);
      setText(result.roots.join("\n"));
      dirtyRef.current = false;
      toast.success(t(($) => $.notes_page.period_brief_collect_roots_saved));
      onClose();
    } catch (error) {
      showErrorToast(
        collectRootsErrorMessage(
          error,
          t(($) => $.notes_page.period_brief_collect_roots_timeout),
        ),
      );
    } finally {
      setSaving(false);
    }
  };

  return (
    <Dialog open onOpenChange={(next) => { if (!next) onClose(); }}>
      <DialogContent
        className="max-w-md"
        data-testid="period-brief-collect-roots-dialog"
      >
        <DialogHeader>
          <DialogTitle>
            {t(($) => $.notes_page.period_brief_collect_roots_title, { label })}
          </DialogTitle>
          <DialogDescription>
            {t(($) => $.notes_page.period_brief_collect_roots_body)}
          </DialogDescription>
        </DialogHeader>
        {notice ? (
          <p
            className="text-sm text-muted-foreground"
            data-testid={
              notice === "timeout"
                ? "period-brief-collect-roots-timeout"
                : "period-brief-collect-roots-offline"
            }
          >
            {notice === "timeout"
              ? t(($) => $.notes_page.period_brief_collect_roots_timeout)
              : t(($) => $.notes_page.period_brief_collect_roots_offline)}
          </p>
        ) : null}
        <textarea
          className="min-h-32 w-full rounded-md border bg-transparent px-3 py-2 text-sm"
          data-testid="period-brief-collect-roots-input"
          disabled={saving}
          value={text}
          onChange={(event) => {
            dirtyRef.current = true;
            setText(event.target.value);
          }}
          placeholder={t(($) => $.notes_page.period_brief_collect_roots_placeholder)}
        />
        <div className="flex justify-end gap-2">
          <Button
            type="button"
            variant="ghost"
            data-testid="period-brief-collect-roots-cancel"
            onClick={onClose}
          >
            {t(($) => $.notes_page.period_brief_cancel)}
          </Button>
          <Button
            type="button"
            data-testid="period-brief-collect-roots-save"
            disabled={saving}
            onClick={() => void save()}
          >
            {t(($) => $.notes_page.period_brief_collect_roots_save)}
          </Button>
        </div>
      </DialogContent>
    </Dialog>
  );
}
