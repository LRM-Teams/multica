// @vitest-environment node
import { describe, expect, it } from "vitest";
import { formatStageGateRejectReply } from "./stage-gate-confirm";

describe("formatStageGateRejectReply (LRM-840)", () => {
  it("includes trimmed reason when provided", () => {
    expect(formatStageGateRejectReply("  来源权重不够  ")).toBe(
      "驳回确认：来源权重不够",
    );
  });

  it("falls back to a default revision ask when reason is empty", () => {
    expect(formatStageGateRejectReply("")).toBe(
      "驳回确认：请根据意见继续修订调研交付。",
    );
    expect(formatStageGateRejectReply()).toBe(
      "驳回确认：请根据意见继续修订调研交付。",
    );
  });
});
