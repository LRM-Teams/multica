import { describe, expect, it } from "vitest";
import {
  AGENT_USERNAME_MAX_LENGTH,
  validateAgentUsername,
} from "./constants";

// Locks the @handle grammar to the exact backend contract (PUT /api/agents/{id}
// `username`): lowercase alphanumerics in dash-separated segments, no
// leading/trailing/consecutive hyphens, max 32. FE and BE must agree so a
// client-valid handle is never rejected by the server (and vice versa).
describe("validateAgentUsername", () => {
  it("accepts valid kebab handles", () => {
    for (const v of ["alice", "qa-bot", "backend-engineer", "a1", "agent-4-2"]) {
      expect(validateAgentUsername(v)).toBeNull();
    }
  });

  it("rejects empty / whitespace-only", () => {
    expect(validateAgentUsername("")).toBe("empty");
    expect(validateAgentUsername("   ")).toBe("empty");
  });

  it("rejects over-length (max 32)", () => {
    expect(AGENT_USERNAME_MAX_LENGTH).toBe(32);
    expect(validateAgentUsername("a".repeat(33))).toBe("too_long");
    expect(validateAgentUsername("a".repeat(32))).toBeNull();
  });

  it("rejects leading, trailing, and consecutive hyphens", () => {
    for (const v of ["-bot", "bot-", "qa--bot"]) {
      expect(validateAgentUsername(v)).toBe("invalid_chars");
    }
  });

  it("rejects non-ascii, uppercase, and other separators", () => {
    for (const v of ["阿策", "QA-bot", "qa_bot", "qa bot", "café"]) {
      expect(validateAgentUsername(v)).toBe("invalid_chars");
    }
  });
});
