import { describe, expect, it } from "vitest";
import { createQueryClient } from "./query-client";

// #1276 guard (global). React Query's default networkMode is "online", which
// PAUSES a mutation while offline — it neither runs nor fails, then silently
// resumes on reconnect. For our writes that means "the user thought it didn't
// happen, but it fired later" (see query-client.ts). These assertions pin the
// explicit values so a silent revert to the library default is caught.
// Verified red-on-revert: flipping mutations→"online" (or deleting the line)
// fails the first test; flipping queries fails the second.
describe("createQueryClient — networkMode defaults (#1276)", () => {
  it("pins mutations.networkMode to 'always' — offline writes attempt+fail-fast, never silent-pause/resend", () => {
    const defaults = createQueryClient().getDefaultOptions();
    expect(defaults.mutations?.networkMode).toBe("always");
  });

  it("pins queries.networkMode to 'online' explicitly — no silent library default", () => {
    const defaults = createQueryClient().getDefaultOptions();
    expect(defaults.queries?.networkMode).toBe("online");
  });
});
