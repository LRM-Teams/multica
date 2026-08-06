// @vitest-environment node
import { describe, expect, it } from "vitest";
import { voiceTargetId } from "./voice-target";

/**
 * #838 H0 — with both surfaces storing into ONE map keyed by this function,
 * isolation between targets IS this function's collision-freedom. If two
 * distinct targets ever produced the same key, a failure in one would overwrite
 * (and be retried into) the other.
 */
describe("voiceTargetId (#838)", () => {
  it("separates channels", () => {
    expect(voiceTargetId("chan-a")).not.toBe(voiceTargetId("chan-b"));
  });

  it("separates threads within one channel", () => {
    expect(voiceTargetId("chan-a", "root-1")).not.toBe(voiceTargetId("chan-a", "root-2"));
  });

  it("separates the same thread id across different channels", () => {
    expect(voiceTargetId("chan-a", "root-1")).not.toBe(voiceTargetId("chan-b", "root-1"));
  });

  it("separates a channel from a thread inside it", () => {
    // The top-level composer and a thread composer are different surfaces and
    // must never share one unsent recording.
    expect(voiceTargetId("chan-a")).not.toBe(voiceTargetId("chan-a", "root-1"));
  });

  it("is stable for the same target", () => {
    expect(voiceTargetId("chan-a", "root-1")).toBe(voiceTargetId("chan-a", "root-1"));
  });
});
