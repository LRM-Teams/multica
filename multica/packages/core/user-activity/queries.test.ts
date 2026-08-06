import { describe, expect, it } from "vitest";
import { keepPreviousData } from "@tanstack/react-query";
import { userActivityListOptions } from "./queries";

describe("userActivityListOptions (LRM-424)", () => {
  it("keeps previous rows on tab switch and refetches on remount", () => {
    const opts = userActivityListOptions("ws-1", "all");
    expect(opts.queryKey).toEqual(["user-activity", "ws-1", "list", "all"]);
    expect(opts.placeholderData).toBe(keepPreviousData);
    expect(opts.refetchOnMount).toBe("always");
  });
});
