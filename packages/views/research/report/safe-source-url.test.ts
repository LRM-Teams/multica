// @vitest-environment node
import { describe, expect, it } from "vitest";
import { safeSourceUrl, sourceHost } from "./safe-source-url";

describe("safeSourceUrl", () => {
  it("accepts only absolute HTTP(S) navigation targets", () => {
    expect(safeSourceUrl(" https://example.com/a?q=1 ")).toBe(
      "https://example.com/a?q=1",
    );
    expect(safeSourceUrl("http://example.com")).toBe("http://example.com");
    expect(safeSourceUrl("javascript:alert(1)")).toBeNull();
    expect(safeSourceUrl("data:text/html,boom")).toBeNull();
    expect(safeSourceUrl("/relative/path")).toBeNull();
    expect(safeSourceUrl("not a url")).toBeNull();
  });

  it("derives hosts only from safe URLs", () => {
    expect(sourceHost("https://www.example.com/path")).toBe("example.com");
    expect(sourceHost("javascript:alert(1)")).toBe("");
  });
});
