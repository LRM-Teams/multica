// @vitest-environment node
import { describe, expect, it } from "vitest";
import { ApiError } from "@multica/core/api";
import { isChannelNameTakenError } from "./channel-create-error";

describe("isChannelNameTakenError", () => {
  it("is true for a 409 carrying the channel_name_taken code", () => {
    const err = new ApiError("conflict", 409, "Conflict", {
      code: "channel_name_taken",
      error: "channel name already exists",
    });
    expect(isChannelNameTakenError(err)).toBe(true);
  });

  it("tolerates unknown extra fields (forward-compatible body)", () => {
    const err = new ApiError("conflict", 409, "Conflict", {
      code: "channel_name_taken",
      error: "x",
      detail: { workspace_id: "ws" },
    });
    expect(isChannelNameTakenError(err)).toBe(true);
  });

  it("is false for a 409 with a different code", () => {
    const err = new ApiError("conflict", 409, "Conflict", { code: "something_else" });
    expect(isChannelNameTakenError(err)).toBe(false);
  });

  it("is false for a 409 with no body", () => {
    expect(isChannelNameTakenError(new ApiError("conflict", 409, "Conflict"))).toBe(false);
  });

  it("is false for a non-409 status even with the code", () => {
    const err = new ApiError("server", 500, "Internal", { code: "channel_name_taken" });
    expect(isChannelNameTakenError(err)).toBe(false);
  });

  it("is false for non-ApiError values", () => {
    expect(isChannelNameTakenError(new Error("boom"))).toBe(false);
    expect(isChannelNameTakenError(null)).toBe(false);
    expect(isChannelNameTakenError(undefined)).toBe(false);
  });
});
