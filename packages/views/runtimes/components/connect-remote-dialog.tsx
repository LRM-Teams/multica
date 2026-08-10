"use client";

import { useCallback, useEffect, useRef, useState } from "react";
import { Check, ChevronRight, Copy, Terminal } from "lucide-react";
import { useQueryClient } from "@tanstack/react-query";
import { useWorkspaceId } from "@multica/core/hooks";
import { useConfigStore } from "@multica/core/config";
import { runtimeKeys } from "@multica/core/runtimes/queries";
import { useWSEvent } from "@multica/core/realtime";
import { paths, useWorkspaceSlug } from "@multica/core/paths";
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from "@multica/ui/components/ui/dialog";
import { Button } from "@multica/ui/components/ui/button";
import { CODE_LIGATURE_CLASS } from "@multica/ui/lib/code-style";
import { copyText } from "@multica/ui/lib/clipboard";
import { cn } from "@multica/ui/lib/utils";
import { useNavigation } from "../../navigation/context";
import { useT } from "../../i18n/use-t";
import {
  DAEMON_SETUP_MODES,
  type DaemonSetupMode,
  daemonSetupCommands,
  defaultDaemonSetupMode,
} from "../../common/daemon-setup-commands";

type Step = "instructions" | "success";

export function ConnectRemoteDialog({ onClose }: { onClose: () => void }) {
  const [step, setStep] = useState<Step>("instructions");
  const wsId = useWorkspaceId();
  const slug = useWorkspaceSlug();
  const qc = useQueryClient();
  const navigation = useNavigation();
  const newRuntimeIdRef = useRef<string | null>(null);

  // `multica setup` is one blocking command that handles config + login
  // + install-service via setup; the dialog passively listens for the resulting
  // `daemon:register` WS event and auto-advances to success.
  const handleDaemonRegister = useCallback(
    (payload: unknown) => {
      if (step !== "instructions") return;
      qc.invalidateQueries({ queryKey: runtimeKeys.all(wsId) });
      const p = payload as Record<string, unknown> | null;
      if (p?.runtime_id && typeof p.runtime_id === "string") {
        newRuntimeIdRef.current = p.runtime_id;
      }
      setStep("success");
    },
    [step, qc, wsId],
  );
  useWSEvent("daemon:register", handleDaemonRegister);

  const handleGoToAgents = () => {
    onClose();
    if (slug) {
      navigation.push(paths.workspace(slug).agents());
    }
  };

  const handleGoToRuntime = () => {
    onClose();
    // Former deep-link to orphan `/computers/{runtimeId}` — machine detail
    // now lives on the computers list page (Frank 2026-08-02).
    if (slug) {
      navigation.push(paths.workspace(slug).computers());
    }
  };

  return (
    <Dialog open onOpenChange={(v) => !v && onClose()}>
      <DialogContent className="flex max-h-[85vh] flex-col gap-0 p-0 sm:max-w-lg">
        {step === "instructions" && <InstructionsStep onClose={onClose} />}
        {step === "success" && (
          <SuccessStep
            onGoToAgents={handleGoToAgents}
            onGoToRuntime={
              newRuntimeIdRef.current ? handleGoToRuntime : undefined
            }
          />
        )}
      </DialogContent>
    </Dialog>
  );
}

// ---------------------------------------------------------------------------
// Copy button + code row — mirrors onboarding/CliInstallInstructions
// ---------------------------------------------------------------------------

function CopyButton({ text, ariaLabel }: { text: string; ariaLabel: string }) {
  const [copied, setCopied] = useState(false);

  useEffect(() => {
    if (!copied) return;
    const t = setTimeout(() => setCopied(false), 2000);
    return () => clearTimeout(t);
  }, [copied]);

  const handleCopy = () => {
    void copyText(text).then((ok) => {
      if (ok) setCopied(true);
    });
  };

  return (
    <button
      type="button"
      onClick={handleCopy}
      aria-label={ariaLabel}
      className="shrink-0 rounded p-1 text-muted-foreground transition-colors hover:bg-accent hover:text-foreground focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring"
    >
      {copied ? (
        <Check className="h-3.5 w-3.5 text-success" aria-hidden />
      ) : (
        <Copy className="h-3.5 w-3.5" aria-hidden />
      )}
    </button>
  );
}

function CommandStep({
  n,
  label,
  cmd,
  copyAria,
}: {
  n: number;
  label: string;
  cmd: string;
  copyAria: string;
}) {
  return (
    <div>
      <p className="mb-1.5 text-xs font-medium text-foreground">
        {n}. {label}
      </p>
      <div className="flex items-start gap-2 rounded-lg bg-muted px-3 py-2.5 font-mono text-sm">
        <Terminal
          className="mt-0.5 h-3.5 w-3.5 shrink-0 text-muted-foreground"
          aria-hidden
        />
        <code
          className={cn(
            "min-w-0 flex-1 break-all whitespace-pre-wrap tabular-nums",
            CODE_LIGATURE_CLASS,
          )}
        >
          {cmd}
        </code>
        <CopyButton text={cmd} ariaLabel={copyAria} />
      </div>
    </div>
  );
}

// ---------------------------------------------------------------------------
// Step 1: Instructions
// ---------------------------------------------------------------------------

function InstructionsStep({ onClose }: { onClose: () => void }) {
  const { t } = useT("runtimes");
  const [mode, setMode] = useState<DaemonSetupMode>(() => defaultDaemonSetupMode());
  const environment = useConfigStore((state) => state.environment);
  const daemonServerUrl = useConfigStore((state) => state.daemonServerUrl);
  const daemonAppUrl = useConfigStore((state) => state.daemonAppUrl);
  const computerVersion = useConfigStore((state) => state.computerVersion);
  const { installCmd, setupCmd } = daemonSetupCommands(
    mode,
    useWorkspaceSlug() ?? undefined,
    {
      environment,
      serverUrl: daemonServerUrl,
      appUrl: daemonAppUrl,
      computerVersion,
    },
  );
  return (
    <>
      <DialogHeader className="px-6 pt-6 pb-2">
        <DialogTitle className="text-base text-balance">
          {t(($) => $.connect.title)}
        </DialogTitle>
        <DialogDescription className="text-xs text-balance">
          {t(($) => $.connect.description)}
        </DialogDescription>
      </DialogHeader>

      <div className="min-h-0 flex-1 overflow-y-auto px-6 py-4">
        <div className="space-y-4">
          <SetupModeSelector mode={mode} onChange={setMode} />

          <CommandStep
            n={1}
            label={t(($) => $.connect.step1_label)}
            cmd={installCmd}
            copyAria={t(($) => $.connect.copy_aria)}
          />

          <div>
            <CommandStep
              n={2}
              label={t(($) => $.connect.step2_label)}
              cmd={setupCmd}
              copyAria={t(($) => $.connect.copy_aria)}
            />
            <p className="mt-1.5 text-[11px] leading-[1.55] text-muted-foreground">
              {t(($) => $.connect.step2_hint)}
            </p>
          </div>

          <WaitingStatus />

          <TroubleDetails />
        </div>
      </div>

      <DialogFooter className="m-0 rounded-b-xl border-t bg-muted/30 px-6 py-3">
        <Button variant="outline" size="sm" onClick={onClose}>
          {t(($) => $.connect.cancel)}
        </Button>
        {/* Done stays disabled until daemon:register replaces this panel with success. */}
        <Button size="sm" disabled>
          {t(($) => $.connect.done)}
        </Button>
      </DialogFooter>
    </>
  );
}

// LRM-1129 freeze ① / LRM-1176: OS sits on the same row as the commands
// heading (Frank CONNECT COMMAND layout). mode_label stays aria-only;
// mode_hints is removed from the visible layer (and locale bundles).
function SetupModeSelector({
  mode,
  onChange,
}: {
  mode: DaemonSetupMode;
  onChange: (mode: DaemonSetupMode) => void;
}) {
  const { t } = useT("runtimes");
  return (
    <div className="flex flex-col gap-1.5 sm:flex-row sm:items-center sm:justify-between sm:gap-3">
      <div className="flex items-center gap-1.5 text-[11px] font-semibold tracking-wide text-muted-foreground uppercase">
        <Terminal className="h-3 w-3 shrink-0" aria-hidden />
        {t(($) => $.connect.commands_heading)}
      </div>
      <div
        className="grid grid-cols-2 gap-1 rounded-lg bg-muted p-[3px] sm:grid-cols-[auto_auto]"
        role="radiogroup"
        aria-label={t(($) => $.connect.mode_label)}
      >
        {DAEMON_SETUP_MODES.map((item) => (
          <button
            key={item}
            type="button"
            role="radio"
            aria-checked={mode === item}
            onClick={() => onChange(item)}
            className={cn(
              "rounded-md px-2.5 py-1 text-[11px] font-medium text-muted-foreground transition-colors",
              mode === item && "bg-background text-foreground shadow-sm",
            )}
          >
            {t(($) => $.connect.modes[item])}
          </button>
        ))}
      </div>
    </div>
  );
}

// LRM-1129 freeze ①: merge install-failure + browser-trouble guidance into
// one <details> after Waiting so steps 1→2 stay consecutive. Host-agnostic
// install tips stay (Parker/#1581); content is not deleted — only re-homed.
function TroubleDetails() {
  const { t } = useT("runtimes");
  // LRM-1199: solid/subtle border — dashed is dropzone/placeholder vocabulary.
  return (
    <details className="group rounded-lg border border-border">
      <summary className="flex cursor-pointer list-none items-center gap-1.5 px-3 py-2 text-xs font-medium text-muted-foreground hover:text-foreground focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring">
        <ChevronRight
          className="h-3 w-3 transition-transform group-open:rotate-90"
          aria-hidden
        />
        {t(($) => $.connect.trouble_summary)}
      </summary>
      <div className="space-y-3 border-t px-3 pt-2.5 pb-3 text-[11px] leading-[1.55] text-muted-foreground">
        <div>
          <h4 className="text-[11px] font-medium text-foreground">
            {t(($) => $.connect.install_trouble_summary)}
          </h4>
          <ul className="mt-1.5 space-y-1.5">
            <li>{t(($) => $.connect.install_trouble_retry)}</li>
            <li>{t(($) => $.connect.install_trouble_network)}</li>
          </ul>
        </div>
        <div className="border-t pt-2.5">
          <h4 className="text-[11px] font-medium text-foreground">
            {t(($) => $.connect.troubleshooting)}
          </h4>
          <div className="mt-1.5 space-y-2">
            <p>{t(($) => $.connect.trouble_intro)}</p>
            <ul className="space-y-1">
              <li className="flex items-center gap-1.5">
                <span>{t(($) => $.connect.trouble_check_status)}</span>
                {/* CLI command — literal shell string, not i18n content. */}
                <code
                  className={cn(
                    "rounded bg-muted px-1.5 py-0.5 font-mono text-[10px] text-foreground",
                    CODE_LIGATURE_CLASS,
                  )}
                >
                  {"multica computer status"}
                </code>
              </li>
              <li className="flex items-center gap-1.5">
                <span>{t(($) => $.connect.trouble_view_logs)}</span>
                {/* CLI command — literal shell string, not i18n content. */}
                <code
                  className={cn(
                    "rounded bg-muted px-1.5 py-0.5 font-mono text-[10px] text-foreground",
                    CODE_LIGATURE_CLASS,
                  )}
                >
                  {"multica computer logs -f"}
                </code>
              </li>
            </ul>
          </div>
        </div>
      </div>
    </details>
  );
}

// ---------------------------------------------------------------------------
// Waiting indicator (LRM-1129 freeze v2 — brand-soft, not success green)
// ---------------------------------------------------------------------------

function WaitingStatus() {
  const { t } = useT("runtimes");
  return (
    <div
      className="flex items-start gap-2.5 rounded-lg border border-brand/30 bg-brand/5 px-3 py-2.5 text-xs"
      role="status"
      aria-live="polite"
    >
      <span className="relative mt-1 inline-flex shrink-0" aria-hidden>
        <span className="absolute inline-flex h-2 w-2 animate-ping rounded-full bg-brand opacity-60 motion-reduce:hidden" />
        <span className="relative inline-flex h-2 w-2 rounded-full bg-brand" />
      </span>
      <div className="min-w-0 space-y-0.5">
        <p className="font-medium text-foreground">
          {t(($) => $.connect.live_listening)}
        </p>
        <p className="leading-[1.4] text-muted-foreground">
          {t(($) => $.connect.live_listening_hint)}
        </p>
      </div>
    </div>
  );
}

// ---------------------------------------------------------------------------
// Step 2: Success
// ---------------------------------------------------------------------------

function SuccessStep({
  onGoToAgents,
  onGoToRuntime,
}: {
  onGoToAgents: () => void;
  onGoToRuntime?: () => void;
}) {
  const { t } = useT("runtimes");
  const primaryRef = useRef<HTMLButtonElement>(null);

  useEffect(() => {
    primaryRef.current?.focus();
  }, []);

  return (
    <>
      <DialogHeader className="px-6 pt-6 pb-2">
        <DialogTitle className="text-base text-balance">
          {t(($) => $.connect.success_title)}
        </DialogTitle>
        <DialogDescription className="text-xs text-balance">
          {t(($) => $.connect.success_description)}
        </DialogDescription>
      </DialogHeader>

      <div className="flex flex-col items-center gap-3 px-6 py-8">
        <div
          className="flex h-12 w-12 items-center justify-center rounded-full bg-success/10"
          aria-hidden
        >
          <Check className="h-6 w-6 text-success" />
        </div>
      </div>

      <DialogFooter className="m-0 rounded-b-xl border-t bg-muted/30 px-6 py-3">
        {onGoToRuntime && (
          <Button variant="ghost" size="sm" onClick={onGoToRuntime}>
            {t(($) => $.connect.view_runtime)}
          </Button>
        )}
        <Button ref={primaryRef} size="sm" onClick={onGoToAgents}>
          {t(($) => $.connect.create_agent)}
          <ChevronRight className="h-3.5 w-3.5" aria-hidden />
        </Button>
      </DialogFooter>
    </>
  );
}
