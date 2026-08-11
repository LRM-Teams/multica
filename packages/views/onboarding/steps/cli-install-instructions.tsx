"use client";

import { useState } from "react";
import { useQuery } from "@tanstack/react-query";
import { Card, CardContent } from "@multica/ui/components/ui/card";
import { cn } from "@multica/ui/lib/utils";
import { useConfigStore } from "@multica/core/config";
import { testComputerReleaseOptions } from "@multica/core/releases/computer-metainfo";
import { useT } from "../../i18n/use-t";
import {
  DAEMON_SETUP_MODES,
  type DaemonSetupMode,
  defaultDaemonSetupMode,
} from "../../common/daemon-setup-commands";
import { DaemonSetupCommandSteps } from "../../common/daemon-setup-command-steps";

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
  return (
    <Card className="w-full">
      <CardContent className="space-y-4 pt-4">
        <SetupModeSelector mode={mode} onChange={setMode} />
        <DaemonSetupCommandSteps
          mode={mode}
          workspaceSlug={workspaceSlug}
          target={{
            environment,
            serverUrl: daemonServerUrl,
            appUrl: daemonAppUrl,
            computerVersion,
          }}
          installLabel={t(($) => $.cli_install.step1_label)}
          setupLabel={t(($) => $.cli_install.step2_label)}
          setupHint={
            environment === "test"
              ? t(($) => $.cli_install.step2_hint_test)
              : t(($) => $.cli_install.step2_hint_production)
          }
          copyAria={t(($) => $.cli_install.copy_aria)}
          installState={
            testReleaseUnavailable
              ? testReleaseError
                ? "error"
                : "loading"
              : undefined
          }
          installErrorLabel={t(($) => $.cli_install.test_release_failed)}
          installRetryLabel={t(($) => $.cli_install.test_release_retry)}
          onInstallRetry={
            testReleaseError
              ? () => void refetchTestRelease()
              : undefined
          }
        />
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
