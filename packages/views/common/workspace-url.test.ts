import { describe, expect, it } from "vitest";
import { workspaceURLPrefix } from "./workspace-url";

describe("workspaceURLPrefix", () => {
  it("uses the configured production app host", () => {
    expect(workspaceURLPrefix("https://www.leagent.me")).toBe(
      "www.leagent.me/",
    );
  });

  it("shows the configured test host", () => {
    expect(workspaceURLPrefix("https://82.157.184.89/")).toBe(
      "82.157.184.89/",
    );
  });

  it("preserves a non-default port", () => {
    expect(workspaceURLPrefix("https://test.leagent.me:8443/app")).toBe(
      "test.leagent.me:8443/",
    );
  });

  it("does not invent a host when the app URL is unavailable", () => {
    expect(workspaceURLPrefix("")).toBe("");
  });
});
