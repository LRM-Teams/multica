import { describe, expect, it } from "vitest";
import { ApiError } from "@multica/core/api";
import { resolveDeleteChannelErrorKey } from "./channel-delete-error";

describe("resolveDeleteChannelErrorKey (LRM-449)", () => {
  it("maps system_channel_protected code", () => {
    const err = new ApiError("system channel is managed automatically", 409, "Conflict", {
      code: "system_channel_protected",
      error: "system channel is managed automatically",
    });
    expect(resolveDeleteChannelErrorKey(err)).toBe("toast_system_protected");
  });

  it("maps channel_delete_dm code", () => {
    const err = new ApiError("direct messages cannot be permanently deleted", 403, "Forbidden", {
      code: "channel_delete_dm",
    });
    expect(resolveDeleteChannelErrorKey(err)).toBe("toast_dm_forbidden");
  });

  it("maps plain 403 to toast_forbidden", () => {
    const err = new ApiError("insufficient permissions", 403, "Forbidden");
    expect(resolveDeleteChannelErrorKey(err)).toBe("toast_forbidden");
  });

  it("maps 404 to toast_not_found", () => {
    const err = new ApiError("channel not found", 404, "Not Found");
    expect(resolveDeleteChannelErrorKey(err)).toBe("toast_not_found");
  });

  it("falls back to toast_failed for unknown errors", () => {
    expect(resolveDeleteChannelErrorKey(new Error("boom"))).toBe("toast_failed");
    expect(resolveDeleteChannelErrorKey(new ApiError("failed to delete channel", 500, "Error"))).toBe(
      "toast_failed",
    );
  });
});
