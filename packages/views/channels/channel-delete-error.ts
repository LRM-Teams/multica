import { ApiError } from "@multica/core/api";

type DeleteChannelErrorCode =
  | "system_channel_protected"
  | "channel_delete_dm"
  | "channel_delete_not_group"
  | "channel_delete_blocked";

function readErrorCode(body: unknown): DeleteChannelErrorCode | null {
  if (!body || typeof body !== "object") return null;
  const code = (body as { code?: unknown }).code;
  if (
    code === "system_channel_protected" ||
    code === "channel_delete_dm" ||
    code === "channel_delete_not_group" ||
    code === "channel_delete_blocked"
  ) {
    return code;
  }
  return null;
}

/**
 * LRM-449 — map permanent-delete failures to a localised reason. Prefer stable
 * `code` from the API; fall back to status. Never surface only a generic
 * "Failed to delete channel" when the server gave a concrete reason.
 */
export function resolveDeleteChannelErrorKey(
  err: unknown,
):
  | "toast_forbidden"
  | "toast_system_protected"
  | "toast_dm_forbidden"
  | "toast_not_group"
  | "toast_blocked"
  | "toast_not_found"
  | "toast_failed" {
  if (!(err instanceof ApiError)) return "toast_failed";

  const code = readErrorCode(err.body);
  switch (code) {
    case "system_channel_protected":
      return "toast_system_protected";
    case "channel_delete_dm":
      return "toast_dm_forbidden";
    case "channel_delete_not_group":
      return "toast_not_group";
    case "channel_delete_blocked":
      return "toast_blocked";
    default:
      break;
  }

  if (err.status === 403) return "toast_forbidden";
  if (err.status === 404) return "toast_not_found";
  if (err.status === 409) return "toast_blocked";
  return "toast_failed";
}
