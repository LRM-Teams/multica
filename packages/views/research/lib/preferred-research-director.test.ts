import { describe, expect, it } from "vitest";
import { preferredResearchDirectorId } from "./preferred-research-director";

describe("preferredResearchDirectorId", () => {
  it("prefers Ronaldo by name over the first listed agent", () => {
    expect(
      preferredResearchDirectorId([
        { id: "first", name: "scout", display_name: "Scout" },
        { id: "ronaldo", name: "luo-na-er-duo", display_name: "罗纳尔多" },
      ]),
    ).toBe("ronaldo");
  });

  it("does not silently assign another agent when no Ronaldo exists", () => {
    expect(
      preferredResearchDirectorId([
        { id: "first", name: "director-one", display_name: "Director One" },
      ]),
    ).toBe("");
  });
});
