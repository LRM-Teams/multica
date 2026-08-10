import {
  multicaInstallCommand,
  type MulticaInstallMode,
} from "@multica/core/constants/repository";

export function landingCLICommands({
  environment,
  appUrl,
  apiUrl,
  computerVersion = "",
  mode = "unix",
}: {
  environment: "production" | "test";
  appUrl: string;
  apiUrl: string;
  computerVersion?: string;
  mode?: MulticaInstallMode;
}) {
  const installCmd = multicaInstallCommand(mode, environment, computerVersion);
  const workspace = "/<workspace-slug>";
  const setupCmd =
    environment === "test"
      ? `multica setup --environment test --server-url ${apiUrl.replace(/\/+$/, "")} --app-url ${appUrl.replace(/\/+$/, "")} ${workspace}`
      : `multica setup ${workspace}`;

  return { installCmd, setupCmd };
}
