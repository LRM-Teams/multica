import { useCallback, useEffect, useRef, useState } from "react";
import { CheckCircle2, Loader2, RotateCcw, XCircle } from "lucide-react";
import { Button } from "@multica/ui/components/ui/button";
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
import { api } from "@multica/core/api";
import type { RuntimeRestartStatus } from "@multica/core/types";
import { useT } from "../../i18n/use-t";

const statusConfig: Record<
  RuntimeRestartStatus,
  { icon: typeof Loader2; color: string }
> = {
  pending: { icon: Loader2, color: "text-muted-foreground" },
  delivered: { icon: CheckCircle2, color: "text-success" },
  timeout: { icon: XCircle, color: "text-warning" },
};

interface RestartSectionProps {
  runtimeId: string;
  isOnline: boolean;
  /**
   * Same eligibility bar as UpdateSection's own action: workspace admin or
   * runtime owner, not a Desktop-managed or sandbox-managed daemon. Restart
   * is disruptive (kills whatever the daemon's agents are mid-task), so it
   * gets the same permission floor as pushing a CLI update, not a looser one.
   */
  canRestart: boolean;
}

export function RestartSection({
  runtimeId,
  isOnline,
  canRestart,
}: RestartSectionProps) {
  const { t } = useT("runtimes");
  const [confirmOpen, setConfirmOpen] = useState(false);
  const [status, setStatus] = useState<RuntimeRestartStatus | null>(null);
  const [error, setError] = useState("");
  const [restarting, setRestarting] = useState(false);
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

  const handleRestart = async () => {
    cleanup();
    setConfirmOpen(false);
    setRestarting(true);
    setStatus("pending");
    setError("");

    try {
      const restart = await api.initiateRestart(runtimeId);

      pollRef.current = setInterval(async () => {
        try {
          const result = await api.getRestart(runtimeId, restart.id);
          setStatus(result.status);

          if (result.status === "delivered") {
            setRestarting(false);
            cleanup();
          } else if (result.status === "timeout") {
            setError(t(($) => $.restart.status.timeout));
            setRestarting(false);
            cleanup();
          }
        } catch {
          // ignore poll errors
        }
      }, 2000);
    } catch {
      setStatus("timeout");
      setError(t(($) => $.restart.initiate_failed));
      setRestarting(false);
    }
  };

  const config = status ? statusConfig[status] : null;
  const Icon = config?.icon;
  const isActive = restarting || status === "pending";
  const statusLabel = status ? t(($) => $.restart.status[status]) : null;
  const canStartRestart = canRestart && isOnline && !isActive;

  return (
    <div className="flex flex-wrap items-center gap-2">
      <Button
        variant="outline"
        size="xs"
        disabled={!canStartRestart}
        onClick={() => setConfirmOpen(true)}
      >
        <RotateCcw className="h-3 w-3" />
        {t(($) => $.restart.action)}
      </Button>

      {status === "timeout" ? (
        <span className="inline-flex items-center gap-1 text-xs text-destructive">
          <XCircle className="h-3 w-3" />
          {error || statusLabel}
        </span>
      ) : (
        config &&
        Icon &&
        statusLabel && (
          <span
            className={`inline-flex items-center gap-1 text-xs ${config.color}`}
          >
            <Icon className={`h-3 w-3 ${isActive ? "animate-spin" : ""}`} />
            {statusLabel}
          </span>
        )
      )}

      <AlertDialog open={confirmOpen} onOpenChange={setConfirmOpen}>
        <AlertDialogContent>
          <AlertDialogHeader>
            <AlertDialogTitle>{t(($) => $.restart.confirm_title)}</AlertDialogTitle>
            <AlertDialogDescription>
              {t(($) => $.restart.confirm_description)}
            </AlertDialogDescription>
          </AlertDialogHeader>
          <AlertDialogFooter>
            <AlertDialogCancel>
              {t(($) => $.restart.confirm_cancel)}
            </AlertDialogCancel>
            <AlertDialogAction variant="destructive" onClick={handleRestart}>
              {t(($) => $.restart.confirm_action)}
            </AlertDialogAction>
          </AlertDialogFooter>
        </AlertDialogContent>
      </AlertDialog>
    </div>
  );
}
