import type { useT } from "../../i18n/use-t";

type RuntimeUpdateTranslator = ReturnType<typeof useT<"runtimes">>["t"];

export function formatRuntimeUpdateError({
  rawError,
  currentVersion,
  targetVersion,
  t,
}: {
  rawError: string | null | undefined;
  currentVersion: string | null | undefined;
  targetVersion: string | null | undefined;
  t: RuntimeUpdateTranslator;
}): string {
  const reason = rawError?.trim();
  if (!reason) return "";
  if (
    reason === "no_current_socket" ||
    reason === "Computer upgrade needs the current Binding socket"
  ) {
    return "Update failed because the Workspace Daemon is not connected. Reconnect it, then retry.";
  }
  const versionMismatch =
    reason === "old_version_reported_after_update" ||
    reason.startsWith("binary_version_mismatch_after_update");
  if (versionMismatch && targetVersion) {
    return t(($) => $.update.last_update_not_applied, {
      currentVersion: currentVersion ?? t(($) => $.update.version_unknown),
      targetVersion,
    });
  }
  return reason;
}
