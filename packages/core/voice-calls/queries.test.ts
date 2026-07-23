import { describe, expect, it } from "vitest";
import { voiceCallKeys, voiceCallOptions } from "./queries";

describe("voice call queries", () => {
  it("scopes call details by workspace and call identity", () => {
    expect(voiceCallKeys.detail("workspace-1", "call-1")).toEqual([
      "voice-calls",
      "workspace-1",
      "detail",
      "call-1",
    ]);
    expect(voiceCallKeys.detail("workspace-2", "call-1"))
      .not.toEqual(voiceCallKeys.detail("workspace-1", "call-1"));
  });

  it("does not fetch until both scope identifiers exist", () => {
    expect(voiceCallOptions("", "call-1").enabled).toBe(false);
    expect(voiceCallOptions("workspace-1", "").enabled).toBe(false);
    expect(voiceCallOptions("workspace-1", "call-1").enabled).toBe(true);
  });
});
