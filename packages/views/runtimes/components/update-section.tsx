import { useState, useEffect, useCallback, useRef } from "react";
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
import { copyText } from "@multica/ui/lib/clipboard";
import { CODE_LIGATURE_CLASS } from "@multica/ui/lib/code-style";
import { cn } from "@multica/ui/lib/utils";
import { MULTICA_LATEST_RELEASE_API_URL } from "@multica/core/constants/repository";
import type { RuntimeUpdateStatus } from "@multica/core/types";
import { useT } from "../../i18n/use-t";

const CACHE_TTL_MS = 10 * 60 * 1000; // 10 minutes

const MANUAL_UPDATE_COMMANDS = [
  {
    key: "brew",
    command: "brew upgrade multica-ai/tap/multica && multica daemon restart",
  },
  {
    key: "linux_script",
    command:
      "curl -fsSL https://raw.githubusercontent.com/multica-ai/multica/main/scripts/install.sh | bash && multica daemon restart",
  },
  {
    key: "windows",
    command:
      "irm https://raw.githubusercontent.com/multica-ai/multica/main/scripts/install.ps1 | iex; multica daemon restart",
  },
] as const;

let cachedLatestVersion: string | null = null;
let cachedAt = 0;

async function fetchLatestVersion(): Promise<string | null> {
  if (cachedLatestVersion && Date.now() - cachedAt < CACHE_TTL_MS) {
    return cachedLatestVersion;
  }
  try {
    const resp = await fetch(MULTICA_LATEST_RELEASE_API_URL, {
      headers: { Accept: "application/vnd.github+json" },
    });
    if (!resp.ok) return null;
    const data = await resp.json();
    cachedLatestVersion = data.tag_name ?? null;
    cachedAt = Date.now();
    return cachedLatestVersion;
  } catch {
    return null;
  }
}

function stripV(v: string): string {
  return v.replace(/^v/, "");
}

function isNewer(latest: string, current: string): boolean {
  const l = stripV(latest).split(".").map(Number);
  const c = stripV(current).split(".").map(Number);
  for (let i = 0; i < Math.max(l.length, c.length); i++) {
    const lv = l[i] ?? 0;
    const cv = c[i] ?? 0;
    if (lv > cv) return true;
    if (lv < cv) return false;
  }
  return false;
}

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

interface UpdateSectionProps {
  runtimeId: string;
  currentVersion: string | null;
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
  isOnline,
  launchedBy,
  canUpdate = true,
}: UpdateSectionProps) {
  const { t } = useT("runtimes");
  const isManaged = launchedBy === "desktop";
  const [manualOpen, setManualOpen] = useState(false);
  const [latestVersion, setLatestVersion] = useState<string | null>(null);
  const [status, setStatus] = useState<RuntimeUpdateStatus | null>(null);
  const [error, setError] = useState("");
  const [output, setOutput] = useState("");
  const [updating, setUpdating] = useState(false);
  const [targetVersion, setTargetVersion] = useState<string | null>(null);
  const pollRef = useRef<ReturnType<typeof setInterval> | null>(null);

  const cleanup = useCallback(() => {
    if (pollRef.current) {
      clearInterval(pollRef.current);
      pollRef.current = null;
    }
  }, []);

  useEffect(() => cleanup, [cleanup]);

  // Fetch latest version on mount.
  useEffect(() => {
    fetchLatestVersion().then(setLatestVersion);
  }, []);

  const markCompleted = useCallback(
    (message: string) => {
      setStatus("completed");
      setOutput(message);
      setUpdating(false);
      setTargetVersion(null);
      cleanup();
      // Auto-clear status after a few seconds so the UI refreshes to show the
      // new version from the re-fetched runtime data.
      setTimeout(() => setStatus(null), 5000);
    },
    [cleanup],
  );

  useEffect(() => {
    if (!updating || !targetVersion || !currentVersion) return;
    if (!isNewer(targetVersion, currentVersion)) {
      markCompleted(`Updated to ${targetVersion}`);
    }
  }, [currentVersion, markCompleted, targetVersion, updating]);

  const handleUpdate = async () => {
    if (!latestVersion) return;
    cleanup();
    setUpdating(true);
    setTargetVersion(latestVersion);
    setStatus("pending");
    setError("");
    setOutput("");

    try {
      const update = await api.initiateUpdate(runtimeId, latestVersion);

      pollRef.current = setInterval(async () => {
        try {
          const result = await api.getUpdateResult(runtimeId, update.id);
          setStatus(result.status as RuntimeUpdateStatus);

          if (result.status === "completed") {
            markCompleted(
              result.output ?? `Updated to ${targetVersion ?? latestVersion}`,
            );
          } else if (
            result.status === "failed" ||
            result.status === "timeout"
          ) {
            setError(result.error ?? t(($) => $.update.unknown_error));
            setUpdating(false);
            setTargetVersion(null);
            cleanup();
          }
        } catch {
          // ignore poll errors
        }
      }, 2000);
    } catch {
      setStatus("failed");
      setError(t(($) => $.update.initiate_failed));
      setUpdating(false);
      setTargetVersion(null);
    }
  };

  const hasUpdate =
    currentVersion &&
    latestVersion &&
    isNewer(latestVersion, currentVersion);

  const config = status ? statusConfig[status] : null;
  const Icon = config?.icon;
  const isActive = status === "pending" || status === "running";

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
            {!hasUpdate && currentVersion && latestVersion && !status && (
              <span className="inline-flex items-center gap-1 text-xs text-success">
                <Check className="h-3 w-3" />
                {t(($) => $.update.latest)}
              </span>
            )}

            {hasUpdate && !status && (
              <>
                <span className="text-xs text-muted-foreground">→</span>
                <span className="text-xs font-mono text-info">
                  {latestVersion}
                </span>
                <span className="text-xs text-muted-foreground">{t(($) => $.update.available)}</span>
              </>
            )}

            {hasUpdate && isOnline && canUpdate && !status && (
              <Button
                variant="outline"
                size="xs"
                onClick={handleUpdate}
                disabled={updating}
              >
                <ArrowUpCircle className="h-3 w-3" />
                {t(($) => $.update.action)}
              </Button>
            )}
          </>
        )}

        {config && Icon && status && (
          <span
            className={`inline-flex items-center gap-1 text-xs ${config.color}`}
          >
            <Icon className={`h-3 w-3 ${isActive ? "animate-spin" : ""}`} />
            {t(($) => $.update.status[status])}
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

      {(status === "failed" || status === "timeout") && error && (
        <div className="rounded-lg border border-destructive/20 bg-destructive/5 px-3 py-2">
          <p className="text-xs text-destructive">{error}</p>
          {status === "failed" && (
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
