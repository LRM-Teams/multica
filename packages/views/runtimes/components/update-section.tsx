import { useState, useEffect, useCallback, useRef } from "react";
import { useQueryClient } from "@tanstack/react-query";
import {
  Loader2,
  CheckCircle2,
  XCircle,
  ArrowUpCircle,
  Check,
  Copy,
  Terminal,
} from "lucide-react";
import { Button } from "@multica/ui/components/ui/button";
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from "@multica/ui/components/ui/dialog";
import { api } from "@multica/core/api";
import {
  MULTICA_INSTALL_COMMAND,
  MULTICA_POWERSHELL_INSTALL_COMMAND,
} from "@multica/core/constants/repository";
import { copyText } from "@multica/ui/lib/clipboard";
import { CODE_LIGATURE_CLASS } from "@multica/ui/lib/code-style";
import { cn } from "@multica/ui/lib/utils";
import type {
  RuntimeHealthState,
  RuntimeUpdateState,
  RuntimeUpdateStatus,
} from "@multica/core/types";
import { useT } from "../../i18n/use-t";

const MANUAL_UPDATE_COMMANDS = [
  {
    key: "mac_linux",
    command: `${MULTICA_INSTALL_COMMAND} && multica daemon restart`,
  },
  {
    key: "windows",
    command: `${MULTICA_POWERSHELL_INSTALL_COMMAND}; multica daemon restart`,
  },
] as const;

const statusConfig: Record<
  RuntimeUpdateStatus,
  { icon: typeof Loader2; color: string }
> = {
  pending: { icon: Loader2, color: "text-muted-foreground" },
  running: { icon: Loader2, color: "text-info" },
  completed: { icon: CheckCircle2, color: "text-success" },
  failed: { icon: XCircle, color: "text-destructive" },
  timeout: { icon: XCircle, color: "text-warning" },
};

function statusFromUpdateState(
  state: RuntimeUpdateState | undefined,
): RuntimeUpdateStatus | null {
  switch (state) {
    case "pending":
    case "running":
    case "completed":
    case "failed":
      return state;
    case "timed_out":
      return "timeout";
    case "idle":
    case undefined:
      return null;
  }
}

interface UpdateSectionProps {
  runtimeId: string;
  currentVersion: string | null;
  targetVersion: string | null;
  updateState?: RuntimeUpdateState;
  runtimeHealth?: RuntimeHealthState;
  isOnline: boolean;
  /**
   * Non-null when the daemon process was spawned by a managed launcher
   * (e.g. "desktop" for the Electron app). In that case the CLI binary
   * is shipped and upgraded by the launcher itself, so in-app self-update
   * is disabled — upgrading would be clobbered on the next launch anyway.
   */
  launchedBy?: string | null;
  canUpdate?: boolean;
}

export function UpdateSection({
  runtimeId,
  currentVersion,
  targetVersion,
  updateState,
  runtimeHealth = "ok",
  isOnline,
  launchedBy,
  canUpdate = true,
}: UpdateSectionProps) {
  const { t } = useT("runtimes");
  const qc = useQueryClient();
  const isManaged = launchedBy === "desktop";
  const [manualOpen, setManualOpen] = useState(false);
  const [status, setStatus] = useState<RuntimeUpdateStatus | null>(null);
  const [error, setError] = useState("");
  const [output, setOutput] = useState("");
  const [updating, setUpdating] = useState(false);
  const pollRef = useRef<ReturnType<typeof setInterval> | null>(null);

  const cleanup = useCallback(() => {
    if (pollRef.current) {
      clearInterval(pollRef.current);
      pollRef.current = null;
    }
  }, []);

  useEffect(() => {
    return () => {
      cleanup();
    };
  }, [cleanup]);

  const refreshRuntimes = useCallback(() => {
    qc.invalidateQueries({
      predicate: (query) => query.queryKey[0] === "runtimes",
    });
  }, [qc]);

  const markCompleted = useCallback(
    (message: string) => {
      setStatus("completed");
      setOutput(message);
      setUpdating(false);
      cleanup();
      refreshRuntimes();
    },
    [cleanup, refreshRuntimes],
  );

  const handleUpdate = async () => {
    if (!targetVersion) return;
    cleanup();
    setUpdating(true);
    setStatus("pending");
    setError("");
    setOutput("");

    try {
      const update = await api.initiateUpdate(runtimeId, targetVersion);

      pollRef.current = setInterval(async () => {
        try {
          const result = await api.getUpdateResult(runtimeId, update.id);
          setStatus(result.status as RuntimeUpdateStatus);

          if (result.status === "completed") {
            markCompleted(
              result.output ?? t(($) => $.update.status.completed),
            );
          } else if (
            result.status === "failed" ||
            result.status === "timeout"
          ) {
            setError(result.error ?? t(($) => $.update.unknown_error));
            setUpdating(false);
            cleanup();
            refreshRuntimes();
          }
        } catch {
          // ignore poll errors
        }
      }, 2000);
    } catch {
      setStatus("failed");
      setError(t(($) => $.update.initiate_failed));
      setUpdating(false);
    }
  };

  const contractStatus = statusFromUpdateState(updateState);
  const publicRuntimeHealth =
    runtimeHealth === "awaiting_confirmation" ? "updating" : runtimeHealth;
  const derivedStatus =
    status ??
    (publicRuntimeHealth === "updating"
      ? contractStatus === "pending"
        ? "pending"
        : "running"
      : publicRuntimeHealth === "failed"
        ? contractStatus === "timeout"
          ? "timeout"
          : "failed"
        : null);
  const hasUpdate =
    publicRuntimeHealth === "update_available" && !!targetVersion;
  const config = derivedStatus ? statusConfig[derivedStatus] : null;
  const Icon = config?.icon;
  const isActive =
    updating || derivedStatus === "pending" || derivedStatus === "running";
  const statusLabel = derivedStatus
    ? t(($) => $.update.status[derivedStatus])
    : null;
  const healthOnlyLabel =
    !derivedStatus && publicRuntimeHealth === "offline"
      ? t(($) => $.update.offline)
      : null;
  const canStartUpdate =
    hasUpdate && isOnline && canUpdate && !isManaged && !isActive;
  const canRetry =
    !!targetVersion &&
    isOnline &&
    canUpdate &&
    !isManaged &&
    !isActive &&
    (derivedStatus === "failed" || derivedStatus === "timeout");

  return (
    <div className="space-y-2">
      <div className="flex items-center gap-2 flex-wrap">
        <span className="text-xs text-muted-foreground">{t(($) => $.update.cli_version_label)}</span>
        <span className="text-xs font-mono">
          {currentVersion ?? t(($) => $.update.version_unknown)}
        </span>

        {isManaged ? (
          <span
            className="inline-flex items-center gap-1 text-xs text-muted-foreground"
            title={t(($) => $.update.managed_by_desktop_title)}
          >
            {t(($) => $.update.managed_by_desktop)}
          </span>
        ) : (
          <>
            {publicRuntimeHealth === "ok" && currentVersion && !derivedStatus ? (
              <span className="inline-flex items-center gap-1 text-xs text-success">
                <Check className="h-3 w-3" />
                {t(($) => $.update.latest)}
              </span>
            ) : null}

            {hasUpdate && !derivedStatus && (
              <>
                <span className="text-xs text-muted-foreground">→</span>
                <span className="text-xs font-mono text-info">
                  {targetVersion}
                </span>
                <span className="text-xs text-muted-foreground">{t(($) => $.update.available)}</span>
              </>
            )}

            {canStartUpdate && (
              <Button
                variant="outline"
                size="xs"
                onClick={handleUpdate}
                disabled={isActive}
              >
                <ArrowUpCircle className="h-3 w-3" />
                {t(($) => $.update.action)}
              </Button>
            )}
          </>
        )}

        {config && Icon && statusLabel && (
          <span
            className={`inline-flex items-center gap-1 text-xs ${config.color}`}
          >
            <Icon className={`h-3 w-3 ${isActive ? "animate-spin" : ""}`} />
            {statusLabel}
          </span>
        )}
        {healthOnlyLabel && (
          <span className="inline-flex items-center gap-1 text-xs text-muted-foreground">
            <XCircle className="h-3 w-3" />
            {healthOnlyLabel}
          </span>
        )}
      </div>

      {!isManaged && (
        <div className="flex flex-wrap items-center gap-2 text-[11px] leading-[1.55] text-muted-foreground">
          <p>{t(($) => $.update.manual_hint)}</p>
          <Button
            variant="ghost"
            size="xs"
            className="h-6 px-2 text-[11px]"
            onClick={() => setManualOpen(true)}
          >
            {t(($) => $.update.manual_action)}
          </Button>
        </div>
      )}

      {status === "completed" && output && (
        <div className="rounded-lg border bg-success/5 px-3 py-2">
          <p className="text-xs text-success">{output}</p>
        </div>
      )}

      {(derivedStatus === "failed" || derivedStatus === "timeout") && (
        <div className="rounded-lg border border-destructive/20 bg-destructive/5 px-3 py-2">
          <p className="text-xs text-destructive">
            {error || statusLabel || t(($) => $.update.unknown_error)}
          </p>
          {canRetry && (
            <div className="mt-1 flex flex-wrap gap-1">
              <Button variant="ghost" size="xs" onClick={handleUpdate}>
                {t(($) => $.update.retry)}
              </Button>
              <Button
                variant="ghost"
                size="xs"
                onClick={() => setManualOpen(true)}
              >
                {t(($) => $.update.manual_action)}
              </Button>
            </div>
          )}
        </div>
      )}

      <ManualUpdateDialog open={manualOpen} onOpenChange={setManualOpen} />
    </div>
  );
}

function ManualUpdateDialog({
  open,
  onOpenChange,
}: {
  open: boolean;
  onOpenChange: (open: boolean) => void;
}) {
  const { t } = useT("runtimes");

  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent className="sm:max-w-2xl">
        <DialogHeader>
          <DialogTitle>{t(($) => $.update.manual_dialog_title)}</DialogTitle>
          <DialogDescription>
            {t(($) => $.update.manual_dialog_description)}
          </DialogDescription>
        </DialogHeader>

        <div className="space-y-3">
          {MANUAL_UPDATE_COMMANDS.map((entry) => (
            <CommandRow
              key={entry.key}
              label={t(($) => $.update.manual_commands[entry.key])}
              command={entry.command}
              copyLabel={t(($) => $.update.copy_command)}
              copiedLabel={t(($) => $.update.copied)}
            />
          ))}
        </div>

        <p className="text-xs text-muted-foreground">
          {t(($) => $.update.manual_after_restart)}
        </p>

        <DialogFooter>
          <Button variant="outline" onClick={() => onOpenChange(false)}>
            {t(($) => $.update.manual_close)}
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}

function CommandRow({
  label,
  command,
  copyLabel,
  copiedLabel,
}: {
  label: string;
  command: string;
  copyLabel: string;
  copiedLabel: string;
}) {
  const [copied, setCopied] = useState(false);

  useEffect(() => {
    if (!copied) return;
    const timeout = setTimeout(() => setCopied(false), 2000);
    return () => clearTimeout(timeout);
  }, [copied]);

  const handleCopy = () => {
    void copyText(command).then((ok) => {
      if (ok) setCopied(true);
    });
  };

  return (
    <div className="rounded-lg border bg-muted/30 p-3">
      <div className="mb-2 flex items-center gap-2 text-xs font-medium text-foreground">
        <Terminal className="h-3.5 w-3.5 text-muted-foreground" />
        {label}
      </div>
      <div className="flex items-start gap-2 rounded-md bg-background px-3 py-2 ring-1 ring-border/70">
        <code
          className={cn(
            "min-w-0 flex-1 break-all font-mono text-[11px] leading-5 text-foreground",
            CODE_LIGATURE_CLASS,
          )}
        >
          {command}
        </code>
        <button
          type="button"
          onClick={handleCopy}
          aria-label={copied ? copiedLabel : copyLabel}
          className="shrink-0 rounded p-1 text-muted-foreground transition-colors hover:bg-accent hover:text-foreground focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring"
        >
          {copied ? (
            <Check className="h-3.5 w-3.5 text-success" aria-hidden />
          ) : (
            <Copy className="h-3.5 w-3.5" aria-hidden />
          )}
        </button>
      </div>
    </div>
  );
}
