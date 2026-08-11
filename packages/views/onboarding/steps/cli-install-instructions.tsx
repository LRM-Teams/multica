"use client";

import { useState } from "react";
import { useQuery } from "@tanstack/react-query";
import { Check, Copy, RefreshCw, Terminal } from "lucide-react";
import { Card, CardContent } from "@multica/ui/components/ui/card";
import { CODE_LIGATURE_CLASS } from "@multica/ui/lib/code-style";
import { cn } from "@multica/ui/lib/utils";
import { copyText } from "@multica/ui/lib/clipboard";
import { useConfigStore } from "@multica/core/config";
import { testComputerReleaseOptions } from "@multica/core/releases/computer-metainfo";
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

function Step({
  n,
  label,
  cmd,
  releaseState,
  releaseErrorLabel,
  retryLabel,
  onRetry,
}: {
  n: number;
  label: string;
  cmd: string;
  releaseState?: "loading" | "error";
  releaseErrorLabel?: string;
  retryLabel?: string;
  onRetry?: () => void;
}) {
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
          {releaseState === "loading" ? (
            <span
              className="my-0.5 block h-4 w-48 animate-pulse rounded bg-muted-foreground/15"
              aria-hidden
            />
          ) : releaseState === "error" ? (
            releaseErrorLabel
          ) : (
            cmd
          )}
        </code>
        {releaseState ? (
          onRetry && (
            <button
              type="button"
              onClick={onRetry}
              className="shrink-0 rounded p-1 text-muted-foreground transition-colors hover:bg-accent hover:text-foreground"
              aria-label={retryLabel}
            >
              <RefreshCw className="h-3.5 w-3.5" />
            </button>
          )
        ) : (
          <CopyButton text={cmd} />
        )}
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
  const configuredComputerVersion = useConfigStore(
    (state) => state.computerVersion,
  );
  const {
    data: testRelease,
    isError: testReleaseError,
    isFetching: testReleaseFetching,
    refetch: refetchTestRelease,
  } = useQuery(
    testComputerReleaseOptions(environment === "test"),
  );
  const computerVersion =
    environment === "test"
      ? testRelease?.tag ?? ""
      : configuredComputerVersion;
  const testReleaseUnavailable =
    environment === "test" &&
    (!testRelease || testReleaseFetching);
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
    computerVersion,
  });
  return (
    <Card className="w-full">
      <CardContent className="space-y-4 pt-4">
        <SetupModeSelector mode={mode} onChange={setMode} />
        <Step
          n={1}
          label={t(($) => $.cli_install.step1_label)}
          cmd={installCmd}
          releaseState={
            testReleaseUnavailable
              ? testReleaseError
                ? "error"
                : "loading"
              : undefined
          }
          releaseErrorLabel={t(($) => $.cli_install.test_release_failed)}
          retryLabel={t(($) => $.cli_install.test_release_retry)}
          onRetry={
            testReleaseError
              ? () => void refetchTestRelease()
              : undefined
          }
        />
        <div>
          <Step n={2} label={t(($) => $.cli_install.step2_label)} cmd={setupCmd} />
          <p className="mt-1.5 text-[11px] leading-[1.55] text-muted-foreground">
            {environment === "test"
              ? t(($) => $.cli_install.step2_hint_test)
              : t(($) => $.cli_install.step2_hint_production)}
          </p>
        </div>
      </CardContent>
    </Card>
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
    </div>
  );
}
