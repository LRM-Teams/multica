import {
  MULTICA_POWERSHELL_INSTALL_COMMAND,
  multicaInstallCommand,
} from "@multica/core/constants/repository";
import type { ServiceEnvironment } from "@multica/core/config";

export type DaemonSetupMode = "unix" | "windows-powershell";

export const DAEMON_SETUP_MODES: DaemonSetupMode[] = ["unix", "windows-powershell"];

export const POWERSHELL_INSTALL_COMMAND = MULTICA_POWERSHELL_INSTALL_COMMAND;

export interface DaemonSetupCommands {
  installCmd: string;
  setupCmd: string;
}

export interface DaemonSetupTarget {
  environment?: ServiceEnvironment;
  serverUrl?: string;
  appUrl?: string;
}

export function defaultDaemonSetupMode(): DaemonSetupMode {
  if (typeof navigator === "undefined") {
    return "unix";
  }
  const platform = navigator.platform || "";
  const userAgent = navigator.userAgent || "";
  if (/Win/i.test(platform) || /Windows/i.test(userAgent)) {
    return "windows-powershell";
  }
  return "unix";
}

export function normalizeCommandURL(url: string | undefined) {
  return url?.trim().replace(/\/+$/, "") ?? "";
}

export function daemonSetupCommands(
  mode: DaemonSetupMode,
  workspaceSlug?: string,
  target: DaemonSetupTarget = {},
): DaemonSetupCommands {
  const isTest = target.environment === "test";
  const installCmd = multicaInstallCommand(mode, target.environment);

  const workspace = `/${workspaceSlug || "<workspace-slug>"}`;
  const setupCmd =
    isTest
      ? `multica setup --environment test --server-url ${normalizeCommandURL(target.serverUrl) || "<server-url>"} --app-url ${normalizeCommandURL(target.appUrl) || "<app-url>"} ${workspace}`
      : `multica setup ${workspace}`;

  return {
    installCmd,
    // Scope setup to the workspace currently open in the dialog.  A bare
    // command can select a different workspace, and self-host is not this flow.
    setupCmd,
  };
}
