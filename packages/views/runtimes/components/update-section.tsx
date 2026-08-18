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
  Clock,
  Pin,
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
import { ApiError } from "@multica/core/api";
import { useWSEvent } from "@multica/core/realtime";
import type {
  ComputerUpgradeDonePayload,
  ComputerUpgradeProgressPayload,
} from "@multica/core/types";
import { createSafeId } from "@multica/core/utils";
import { showErrorToast } from "@multica/ui/lib/error-toast";
import { multicaInstallCommand } from "@multica/core/constants/repository";
import { useConfigStore } from "@multica/core/config";
import { copyText } from "@multica/ui/lib/clipboard";
import { CODE_LIGATURE_CLASS } from "@multica/ui/lib/code-style";
import { cn } from "@multica/ui/lib/utils";
import type {
  RuntimeHealthState,
  RuntimeUpdateState,
  RuntimeUpdateStatus,
} from "@multica/core/types";
import { useT } from "../../i18n/use-t";
import { formatRuntimeUpdateError } from "./update-error";
import { deriveUpdateStatus, isNewerCliVersion, useComputerUpgrade, useComputerUpgradeStore } from "@multica/core/runtimes";

const statusConfig: Record<
  RuntimeUpdateStatus,
  { icon: typeof Loader2; color: string }
> = {
  queued: { icon: Clock, color: "text-muted-foreground" },
  pending: { icon: Loader2, color: "text-muted-foreground" },
  running: { icon: Loader2, color: "text-brand" },
  completed: { icon: CheckCircle2, color: "text-success" },
  ready_to_apply: { icon: CheckCircle2, color: "text-warning" },
  failed: { icon: XCircle, color: "text-destructive" },
  timeout: { icon: XCircle, color: "text-warning" },
};

interface UpdateSectionProps {
  daemonId: string;
  currentVersion: string | null;
  targetVersion: string | null;
  updateState?: RuntimeUpdateState;
  runtimeHealth?: RuntimeHealthState;
  updateError?: string | null;
  isOnline: boolean;
  /**
   * Non-null when the daemon process was spawned by a managed launcher
   * (e.g. "desktop" for the Electron app). In that case the CLI binary
   * is shipped and upgraded by the launcher itself, so in-app self-update
   * is disabled — upgrading would be clobbered on the next launch anyway.
   */
  launchedBy?: string | null;
  canUpdate?: boolean;
  /**
   * True for a daemon-enabled env-dispatch sandbox (isSandboxRuntime,
   * `metadata.sandbox_instance_id` set). Task #8 (2026-07-31): the sidebar
   * update-attention badge/popover already excludes sandboxes via the
   * canonical `runtimeCanStartSelfUpdate` gate (#1643) — this component's
   * own start/retry eligibility was a separate, hand-rolled boolean that
   * never checked sandbox status, so the same runtime could show "disabled"
   * in the sidebar and a live, clickable button here. Adding just this one
   * missing condition (not swapping the whole gate to
   * `runtimeCanStartSelfUpdate`, which requires `runtime_health ===
   * "update_available"` and would incorrectly block retrying after a
   * failure, when health has already flipped to "failed") closes that
   * specific gap without touching the retry-after-failure path.
   */
  isSandbox?: boolean;
  /**
   * Machine detail Actions zone: no always-on explanatory copy — keep the
   * manual-update control, drop the paragraph under the version row.
   */
  compact?: boolean;
  /**
   * Task #81 (b) — the daemon's locally-recorded pin intent
   * (`MULTICA_PINNED_VERSION`). Parker, 2026-08-02: pin wins over both a
   * server-initiated push (backend, separate) and a manual click here —
   * there is no "click to override" path. Disables start/retry and shows
   * an always-visible reason; unpinning (not this button) is the only way
   * to re-enable upgrading.
   */
  pinnedVersion?: string | null;
}

export function UpdateSection({
  daemonId,
  currentVersion,
  targetVersion,
  updateState,
  runtimeHealth = "ok",
  updateError,
  isOnline,
  launchedBy,
  canUpdate = true,
  isSandbox = false,
  compact = false,
  pinnedVersion,
}: UpdateSectionProps) {
  const { t } = useT("runtimes");
  const qc = useQueryClient();
  const isManaged = launchedBy === "desktop";
  const [manualOpen, setManualOpen] = useState(false);
  const [status, setStatus] = useState<RuntimeUpdateStatus | null>(null);
  const [error, setError] = useState("");
  const [output, setOutput] = useState("");
  const [updating, setUpdating] = useState(false);
  const requestIdRef = useRef<string | null>(null);

  const cleanup = useCallback(() => {
    requestIdRef.current = null;
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

  const activeUpgrade = useComputerUpgrade(daemonId);
  const effectiveStatus = (activeUpgrade ? activeUpgrade.phase : null) ?? status;

  useWSEvent("computer:upgrade:progress", (raw) => {
    const payload = raw as ComputerUpgradeProgressPayload;
    if (payload.computer_id !== daemonId) return;
    useComputerUpgradeStore.getState().recordProgress(payload);
    setStatus("running");
    setUpdating(true);
  });
  useWSEvent("computer:upgrade:done", (raw) => {
    const payload = raw as ComputerUpgradeDonePayload;
    if (payload.computer_id !== daemonId) return;
    useComputerUpgradeStore.getState().recordDone(payload);
    if (payload.ok) {
      markCompleted(t(($) => $.update.status.completed));
      return;
    }
    setError(payload.error || t(($) => $.update.unknown_error));
    setStatus("failed");
    setUpdating(false);
    cleanup();
    refreshRuntimes();
  });

  const handleUpdate = async () => {
    if (!targetVersion) return;
    const requestId = createSafeId();
    requestIdRef.current = requestId;
    setUpdating(true);
    setStatus("pending");
    setError("");
    setOutput("");

    try {
      await useComputerUpgradeStore.getState().startUpgrade({
        daemonId,
        targetVersion,
        requestId,
      });
      setStatus("running");
    } catch (err) {
      // Task #81 (b) — the button is disabled whenever we already know the
      // machine is pinned, so this 409 only fires on a genuine bypass (the
      // pin took effect between render and click, or a direct API call).
      // A transient toast, not the persistent failed-state box below: this
      // isn't an update that failed, it never started. Use our own
      // `pinnedVersion` prop rather than parsing the server's message
      // string — the `code` is the only part of the contract we match on
      // (Parker, 2026-08-02).
      if (
        err instanceof ApiError &&
        err.status === 409 &&
        err.body &&
        typeof err.body === "object" &&
        (err.body as Record<string, unknown>).code === "runtime_pinned"
      ) {
        showErrorToast(
          t(($) => $.update.pin_blocked_toast, {
            version: pinnedVersion?.trim() || t(($) => $.update.version_unknown),
          }),
        );
        setUpdating(false);
        return;
      }
      setStatus("failed");
      setError(t(($) => $.update.initiate_failed));
      setUpdating(false);
    }
  };

  const derivedStatus = deriveUpdateStatus({
    pollStatus: effectiveStatus,
    updateState,
    runtimeHealth,
  });
  const hasUpdate =
    runtimeHealth === "update_available" &&
    !!targetVersion &&
    isNewerCliVersion(targetVersion, currentVersion);
  const rawContractError =
    runtimeHealth === "failed" ? (updateError?.trim() ?? "") : "";
  const contractError =
    runtimeHealth === "failed"
      ? formatRuntimeUpdateError({
          rawError: updateError,
          currentVersion,
          targetVersion,
          t,
        })
      : "";
  const showRawReason =
    !!rawContractError && !!contractError && rawContractError !== contractError;
  const config = derivedStatus ? statusConfig[derivedStatus] : null;
  const Icon = config?.icon;
  const isActive =
    updating || derivedStatus === "pending" || derivedStatus === "running";
  const statusLabel = derivedStatus
    ? t(($) => $.update.status[derivedStatus])
    : null;
  const healthOnlyLabel =
    !derivedStatus && runtimeHealth === "offline"
      ? t(($) => $.update.offline)
      : null;
  const isPinned = !!pinnedVersion?.trim();
  const canStartUpdate =
    hasUpdate &&
    !derivedStatus &&
    isOnline &&
    canUpdate &&
    !isManaged &&
    !isSandbox &&
    !isPinned &&
    !isActive;
  const canRetry =
    !!targetVersion &&
    isOnline &&
    canUpdate &&
    !isManaged &&
    !isSandbox &&
    !isPinned &&
    !isActive &&
    (derivedStatus === "failed" || derivedStatus === "timeout");

  return (
    <div className="space-y-2">
      <div className="flex items-center gap-2 flex-wrap">
        {/* Machine detail: version lives in Basics only (Wren/Iris/Frank
            2026-08-02) — compact ops show upgrade/up-to-date controls without
            repeating the CLI version number. */}
        {!compact && (
          <>
            <span className="text-xs text-muted-foreground">
              {t(($) => $.update.cli_version_label)}
            </span>
            <span className="text-xs font-mono">
              {currentVersion ?? t(($) => $.update.version_unknown)}
            </span>
          </>
        )}

        {isManaged ? (
          <span
            className="inline-flex items-center gap-1 text-xs text-muted-foreground"
            title={t(($) => $.update.managed_by_desktop_title)}
          >
            {t(($) => $.update.managed_by_desktop)}
          </span>
        ) : isSandbox ? (
          // Task #8 (2026-07-31, Parker): a disabled state with a reason,
          // never a silently-missing button — the user shouldn't have to
          // guess whether this is broken or intentional.
          <span
            className="inline-flex items-center gap-1 text-xs text-muted-foreground"
            title={t(($) => $.update.managed_by_sandbox_title)}
          >
            {t(($) => $.update.managed_by_sandbox)}
          </span>
        ) : (
          <>
            {!compact && hasUpdate && !derivedStatus && (
              <>
                <span className="text-xs text-muted-foreground">→</span>
                <span className="text-xs font-mono text-brand">
                  {targetVersion}
                </span>
                <span className="text-xs text-muted-foreground">{t(($) => $.update.available)}</span>
              </>
            )}

            {/* Frank, 2026-08-01: a button that vanishes when there's nothing
                to do reads as "broken", not "up to date" — same rule as
                RestartSection's always-rendered, disabled-when-ineligible
                button. When up to date, the button stays but its own label
                carries the reason, so a disabled state never looks unexplained. */}
            {!hasUpdate && !derivedStatus && runtimeHealth === "ok" && currentVersion ? (
              <Button variant="outline" size="xs" disabled>
                <Check className="h-3 w-3" />
                {compact
                  ? t(($) => $.update.up_to_date_short)
                  : t(($) => $.update.up_to_date, { version: currentVersion })}
              </Button>
            ) : (
              hasUpdate && (
                <Button
                  variant="outline"
                  size="xs"
                  onClick={handleUpdate}
                  disabled={!canStartUpdate}
                >
                  <ArrowUpCircle className="h-3 w-3" />
                  {t(($) => $.update.action)}
                </Button>
              )
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
        {/* Task #81 (b) — Parker, 2026-08-02: pin wins over a manual click,
            no override. Only shown when there's actually an update being
            blocked (hasUpdate) — a pinned, already-up-to-date machine has
            nothing for this to explain. Always visible, not hover-only. */}
        {isPinned && hasUpdate && !derivedStatus && (
          <span
            className="inline-flex items-center gap-1 text-xs text-muted-foreground"
            data-testid="update-pin-blocked-reason"
          >
            <Pin className="h-3 w-3" />
            {t(($) => $.update.pin_blocked, { version: pinnedVersion })}
          </span>
        )}
      </div>

      {!isManaged && (
        <div className="flex flex-wrap items-center gap-2 text-[11px] leading-[1.55] text-muted-foreground">
          {!compact && <p>{t(($) => $.update.manual_hint)}</p>}
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

      {derivedStatus === "ready_to_apply" && (
        <div className="rounded-lg border border-warning/20 bg-warning/5 px-3 py-2">
          <p className="text-xs text-warning">
            {statusLabel || t(($) => $.update.status.ready_to_apply)}
          </p>
          <div className="mt-1 flex flex-wrap gap-1">
            <Button
              variant="ghost"
              size="xs"
              onClick={() => setManualOpen(true)}
            >
              {t(($) => $.update.manual_action)}
            </Button>
          </div>
        </div>
      )}

      {(derivedStatus === "failed" || derivedStatus === "timeout") && (
        <div className="rounded-lg border border-destructive/20 bg-destructive/5 px-3 py-2">
          <p
            className="text-xs text-destructive"
            title={updateError ?? undefined}
          >
            {error ||
              contractError ||
              statusLabel ||
              t(($) => $.update.unknown_error)}
          </p>
          {showRawReason && (
            <p className="mt-1 break-all text-[11px] leading-snug text-muted-foreground">
              {rawContractError}
            </p>
          )}
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
  const environment = useConfigStore((state) => state.environment);
  const computerVersion = useConfigStore((state) => state.computerVersion);
  const commands = [
    {
      key: "mac_linux",
      command: `${multicaInstallCommand("unix", environment, computerVersion)} && multica computer restart`,
    },
    {
      key: "windows",
      command: `${multicaInstallCommand("windows-powershell", environment, computerVersion)}; multica computer restart`,
    },
  ] as const;

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
          {commands.map((entry) => (
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
