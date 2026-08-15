import { describe, expect, it } from "vitest";
import { githubRepositoryFromEvidence } from "./goal-bootstrap-utils";

describe("githubRepositoryFromEvidence", () => {
  it("normalizes a GitHub pull request evidence URL to its repository", () => {
    expect(githubRepositoryFromEvidence([
      "test:passed",
      "https://github.com/LRM-Teams/minecraft/pull/13",
    ])).toBe("https://github.com/LRM-Teams/minecraft");
  });

  it("does not trust lookalike hosts or malformed repository paths", () => {
    expect(githubRepositoryFromEvidence([
      "https://github.com.evil.test/LRM-Teams/minecraft/pull/13",
      "https://github.com/LRM-Teams",
    ])).toBe("");
  });
});
