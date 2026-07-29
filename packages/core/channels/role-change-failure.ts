import { ApiError } from "../api";

/**
 * Why an owner-only role change failed (#832 / #847).
 *
 * Each kind gets its own sentence in the UI. They are kept apart because the
 * right thing for the user to DO differs: refresh, wait, retry, or nothing.
 * Collapsing them is how a surface ends up saying something that is true for
 * one case and false for the others — the defect this whole change removes.
 */
export type RoleChangeFailure =
  | "owner_changed"
  | "conflict"
  | "forbidden"
  | "gone"
  | "contract"
  | "transient";

/** Server-side marker for "someone else took ownership mid-flight" (#1326). */
const OWNER_CHANGED_CODE = "owner_changed";

function bodyCode(error: ApiError): string | undefined {
  const body = error.body;
  if (!body || typeof body !== "object") return undefined;
  const code = (body as { code?: unknown }).code;
  return typeof code === "string" ? code : undefined;
}

/**
 * Classify a failed role change.
 *
 * Keys on HTTP status and the stable `body.code` ONLY — never on the server's
 * message. That message is hard-coded Chinese server-side and the server does
 * not know the viewer's locale, so rendering it would show the wrong language
 * and couple our copy to theirs (Felix, #844 boundary). All user-facing wording
 * is supplied by the frontend in four locales.
 *
 * ⚠️ `conflict` means "the server refused with 409 and we cannot tell why" —
 * NOT "the viewer is the sole owner". Read it as an unresolvable state, not as
 * a fact about ownership.
 *
 * There is already more than one 409 on this route: "sole owner must transfer
 * first" AND "channel is archived" (channel.go:1650). Neither carries a stable
 * code, nothing stops a third being added, and a frontend test cannot detect it
 * if one is —
 * there is no shared contract artifact between us and the server. So the UI
 * shows a NEUTRAL message and does not name a cause: naming it would be a guess
 * that reads as a fact, which is the exact defect this change removes. When the
 * backend adds a stable code, this branch keys on it and the specific "transfer
 * ownership first" guidance can be restored.
 */
export function classifyRoleChangeFailure(error: unknown): RoleChangeFailure {
  if (!(error instanceof ApiError)) return "transient";

  if (error.status === 403) {
    // A plain 403 deliberately carries no code; the server keeps them distinct
    // so we can too ("must not collapse", #1326). One means the world moved,
    // the other means you may not do this.
    return bodyCode(error) === OWNER_CHANGED_CODE ? "owner_changed" : "forbidden";
  }
  if (error.status === 409) return "conflict";
  // 404 covers both "channel not found" and "channel member not found" and the
  // status cannot tell them apart — so the copy must not claim which. Saying
  // "that member has left" would be asserting more than the evidence supports.
  if (error.status === 404) return "gone";
  // 400 = the route refused the SHAPE of our request. The server returns it for
  // a malformed channel/member UUID, a member_type outside user|agent, an
  // unparseable body, a `role` outside manager|member, `role: "owner"` on the
  // PATCH (ownership moves only through the dedicated POST route), and — on
  // transfer only — a non-human recipient.
  //
  // Each of those is closed before the request is made: ids come from the
  // roster, `updateChannelMemberRole` types its role parameter as
  // "manager" | "member" so the refused value cannot be constructed, transfer
  // calls the POST route, and `canTransferOwnership` requires
  // member_type === "user". So this kind should be unreachable — which is
  // exactly why it stays distinct from `transient` rather than being retried:
  // if it fires, one of those guarantees has broken, and it should surface by
  // name instead of looking like a network blip.
  if (error.status === 400) return "contract";
  return "transient";
}
