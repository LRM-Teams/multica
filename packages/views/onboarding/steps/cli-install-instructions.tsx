"use client";

import { useState } from "react";
import { Check, ChevronRight, Copy, Terminal } from "lucide-react";
import { Card, CardContent } from "@multica/ui/components/ui/card";
import { CODE_LIGATURE_CLASS } from "@multica/ui/lib/code-style";
import { cn } from "@multica/ui/lib/utils";
import { copyText } from "@multica/ui/lib/clipboard";
import { useConfigStore } from "@multica/core/config";
import { useT } from "../../i18n/use-t";
import {
  DAEMON_SETUP_MODES,
  type DaemonSetupMode,
  daemonSetupCommands,
  defaultDaemonSetupMode,
} from "../../common/daemon-setup-commands";

function CopyButton({ text }: { text: string }) {
  const { t } = useT("onboarding");
  const [copied, setCopied] = useState(false);

  const handleCopy = () => {
    void copyText(text).then((ok) => {
      if (!ok) return;
      setCopied(true);
      setTimeout(() => setCopied(false), 2000);
    });
  };

  return (
    <button
      type="button"
      onClick={handleCopy}
      className="shrink-0 rounded p-1 text-muted-foreground transition-colors hover:bg-accent hover:text-foreground"
      aria-label={t(($) => $.cli_install.copy_aria)}
    >
      {copied ? (
        <Check className="h-3.5 w-3.5 text-success" />
      ) : (
        <Copy className="h-3.5 w-3.5" />
      )}
    </button>
  );
}

function Step({ n, label, cmd }: { n: number; label: string; cmd: string }) {
  return (
    <div>
      <p className="mb-1.5 text-xs font-medium text-foreground">
        {n}. {label}
      </p>
      <div className="flex items-start gap-2 rounded-lg bg-muted px-3 py-2.5 font-mono text-sm">
        <Terminal className="mt-0.5 h-3.5 w-3.5 shrink-0 text-muted-foreground" />
        <code
          className={cn(
            "min-w-0 flex-1 whitespace-pre-wrap break-all",
            CODE_LIGATURE_CLASS,
          )}
        >
          {cmd}
        </code>
        <CopyButton text={cmd} />
      </div>
    </div>
  );
}

/** CLI install instructions for the environment serving the current UI. */
export function CliInstallInstructions({
  mode: controlledMode,
  onModeChange,
  workspaceSlug,
}: {
  mode?: DaemonSetupMode;
  onModeChange?: (mode: DaemonSetupMode) => void;
  /** Prefer the real Workspace slug so `multica setup /<slug>` is copy-ready. */
  workspaceSlug?: string;
} = {}) {
  const { t } = useT("onboarding");
  const environment = useConfigStore((state) => state.environment);
  const daemonServerUrl = useConfigStore((state) => state.daemonServerUrl);
  const daemonAppUrl = useConfigStore((state) => state.daemonAppUrl);
  const [uncontrolledMode, setUncontrolledMode] = useState<DaemonSetupMode>(() =>
    defaultDaemonSetupMode(),
  );
  const mode = controlledMode ?? uncontrolledMode;
  const setMode = (nextMode: DaemonSetupMode) => {
    if (controlledMode === undefined) {
      setUncontrolledMode(nextMode);
    }
    onModeChange?.(nextMode);
  };
  const { installCmd, setupCmd } = daemonSetupCommands(mode, workspaceSlug, {
    environment,
    serverUrl: daemonServerUrl,
    appUrl: daemonAppUrl,
  });
  return (
    <Card className="w-full">
      <CardContent className="space-y-4 pt-4">
        <p className="text-xs leading-[1.55] text-muted-foreground">
          {t(($) => $.cli_install.intro)}
        </p>
        <SetupModeSelector mode={mode} onChange={setMode} />
        <Step n={1} label={t(($) => $.cli_install.step1_label)} cmd={installCmd} />
        <div>
          <Step n={2} label={t(($) => $.cli_install.step2_label)} cmd={setupCmd} />
          <p className="mt-1.5 text-[11px] leading-[1.55] text-muted-foreground">
            {t(($) => $.cli_install.step2_hint)}
          </p>
        </div>
        <TroubleshootingDetails />
      </CardContent>
    </Card>
  );
}

// No known support inbox/channel exists to link here (checked repo + docs
// before writing this) — self-diagnosis only, don't invent a destination.
function TroubleshootingDetails() {
  const { t } = useT("onboarding");
  return (
    <details className="group rounded-lg border border-dashed">
      <summary className="flex cursor-pointer list-none items-center gap-1.5 px-3 py-2 text-xs font-medium text-muted-foreground hover:text-foreground focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring">
        <ChevronRight
          className="h-3 w-3 transition-transform group-open:rotate-90"
          aria-hidden
        />
        {t(($) => $.cli_install.trouble_summary)}
      </summary>
      <div className="space-y-2 border-t px-3 pt-2.5 pb-3 text-[11px] leading-[1.55] text-muted-foreground">
        <p>{t(($) => $.cli_install.trouble_intro)}</p>
        <ul className="list-disc space-y-1 pl-4">
          <li>{t(($) => $.cli_install.trouble_retry)}</li>
          <li>{t(($) => $.cli_install.trouble_check_network)}</li>
          <li>
            {t(($) => $.cli_install.trouble_check_daemon_prefix)}
            <code
              className={cn(
                "rounded bg-muted px-1.5 py-0.5 font-mono text-[10px] text-foreground",
                CODE_LIGATURE_CLASS,
              )}
            >
              {"multica computer status"}
            </code>
            {" / "}
            {/* CLI command — literal shell string, not i18n content. */}
            <code
              className={cn(
                "rounded bg-muted px-1.5 py-0.5 font-mono text-[10px] text-foreground",
                CODE_LIGATURE_CLASS,
              )}
            >
              {"multica computer logs -f"}
            </code>
            {t(($) => $.cli_install.trouble_check_daemon_suffix)}
          </li>
        </ul>
      </div>
    </details>
  );
}

function SetupModeSelector({
  mode,
  onChange,
}: {
  mode: DaemonSetupMode;
  onChange: (mode: DaemonSetupMode) => void;
}) {
  const { t } = useT("onboarding");
  return (
    <div className="space-y-2">
      <div className="text-xs font-medium text-foreground">
        {t(($) => $.cli_install.mode_label)}
      </div>
      <div
        className="grid grid-cols-1 gap-1 rounded-lg bg-muted p-1 sm:grid-cols-2"
        role="radiogroup"
        aria-label={t(($) => $.cli_install.mode_label)}
      >
        {DAEMON_SETUP_MODES.map((item) => (
          <button
            key={item}
            type="button"
            role="radio"
            aria-checked={mode === item}
            onClick={() => onChange(item)}
            className={cn(
              "rounded-md px-3 py-1.5 text-xs font-medium text-muted-foreground transition-colors",
              mode === item && "bg-background text-foreground shadow-sm",
            )}
          >
            {t(($) => $.cli_install.modes[item])}
          </button>
        ))}
      </div>
      <p className="text-[11px] leading-[1.55] text-muted-foreground">
        {t(($) => $.cli_install.mode_hints[mode])}
      </p>
    </div>
  );
}
