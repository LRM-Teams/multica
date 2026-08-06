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
