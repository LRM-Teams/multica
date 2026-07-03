import { ApiError, ChannelCreateErrorBodySchema } from "@multica/core/api";

/**
 * True when a channel-create request failed because the (workspace, name) pair
 * already exists. The backend signals this as a 409 with a stable machine code
 * (`channel_name_taken`); we branch on the code, never on the English message,
 * so the FE can localise the message itself. Anything else (non-409, missing or
 * unrecognised body) returns false and falls through to the generic error.
 */
export function isChannelNameTakenError(err: unknown): boolean {
  if (!(err instanceof ApiError) || err.status !== 409) return false;
  return ChannelCreateErrorBodySchema.safeParse(err.body).success;
}
