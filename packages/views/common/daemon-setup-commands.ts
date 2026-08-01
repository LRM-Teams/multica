import {
  MULTICA_INSTALL_COMMAND,
  MULTICA_POWERSHELL_INSTALL_COMMAND,
} from "@multica/core/constants/repository";

export type DaemonSetupMode = "unix" | "windows-powershell";

export const DAEMON_SETUP_MODES: DaemonSetupMode[] = ["unix", "windows-powershell"];

export const POWERSHELL_INSTALL_COMMAND = MULTICA_POWERSHELL_INSTALL_COMMAND;

export interface DaemonSetupCommands {
  installCmd: string;
  setupCmd: string;
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
  serverUrl: string | undefined,
  appUrl: string | undefined,
  mode: DaemonSetupMode,
): DaemonSetupCommands {
  const normalizedServerUrl = normalizeCommandURL(serverUrl);
  const normalizedAppUrl = normalizeCommandURL(appUrl);
  const installCmd =
    mode === "windows-powershell"
      ? POWERSHELL_INSTALL_COMMAND
      : MULTICA_INSTALL_COMMAND;

  if (normalizedServerUrl && normalizedAppUrl) {
    return {
      installCmd,
      setupCmd: [
        "multica setup self-host",
        `--server-url ${normalizedServerUrl}`,
        `--app-url ${normalizedAppUrl}`,
      ].join(" "),
    };
  }

  return {
    installCmd,
    setupCmd: "multica setup",
  };
}
