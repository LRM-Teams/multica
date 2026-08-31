// @vitest-environment node
import { describe, expect, it } from "vitest";
import { isTransportHangError } from "./timeline-error";

describe("isTransportHangError", () => {
  it("matches Cursor HTTP/2 keepalive dumps", () => {
    expect(
      isTransportHangError(
        "Error: RetriableError: [internal] HTTP/2 keepalive ping timed out after 5000ms",
      ),
    ).toBe(true);
  });

  it("ignores ordinary agent text", () => {
    expect(isTransportHangError("改动已就绪，开始提交并开 PR。")).toBe(false);
    expect(isTransportHangError("")).toBe(false);
  });
});
