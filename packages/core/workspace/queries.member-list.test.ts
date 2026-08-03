import { describe, expect, it } from "vitest";
import { memberListOptions } from "./queries";

describe("memberListOptions", () => {
  it("keeps navigation remounts warm between realtime invalidations", () => {
    const options = memberListOptions("workspace-1");

    expect(options.queryKey).toEqual(["workspaces", "workspace-1", "members"]);
    expect(options.staleTime).toBe(5 * 60 * 1000);
  });
});
