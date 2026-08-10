import {
  multicaInstallCommand,
  type MulticaInstallMode,
} from "@multica/core/constants/repository";

export function landingCLICommands({
  environment,
  appUrl,
  apiUrl,
  mode = "unix",
}: {
  environment: "production" | "test";
  appUrl: string;
  apiUrl: string;
  mode?: MulticaInstallMode;
}) {
  const installCmd = multicaInstallCommand(mode, environment);
  const workspace = "/<workspace-slug>";
  const setupCmd =
    environment === "test"
      ? `multica setup --environment test --server-url ${apiUrl.replace(/\/+$/, "")} --app-url ${appUrl.replace(/\/+$/, "")} ${workspace}`
      : `multica setup ${workspace}`;

  return { installCmd, setupCmd };
}
