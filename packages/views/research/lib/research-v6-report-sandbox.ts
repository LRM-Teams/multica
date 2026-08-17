export type ResearchV6ReportSandboxUrlVerdict =
  | { ok: true; url: string }
  | {
      ok: false;
      reason:
        | "empty"
        | "missing_app_origin"
        | "invalid"
        | "unsupported_protocol"
        | "embedded_credentials"
        | "same_origin";
    };

/** Fail-closed URL check before mounting a capability-bearing report iframe. */
export function validateResearchV6ReportSandboxUrl(
  rawUrl: string,
  appOrigin: string,
): ResearchV6ReportSandboxUrlVerdict {
  if (!rawUrl.trim()) return { ok: false, reason: "empty" };
  if (!appOrigin.trim()) return { ok: false, reason: "missing_app_origin" };

  let url: URL;
  let application: URL;
  try {
    url = new URL(rawUrl);
    application = new URL(appOrigin);
  } catch {
    return { ok: false, reason: "invalid" };
  }
  if (url.protocol !== "https:") {
    return { ok: false, reason: "unsupported_protocol" };
  }
  if (url.username || url.password) {
    return { ok: false, reason: "embedded_credentials" };
  }
  if (url.origin === application.origin) {
    return { ok: false, reason: "same_origin" };
  }
  return { ok: true, url: url.toString() };
}
