// @vitest-environment node
import { describe, it, expect } from "vitest";
import { ApiError } from "@multica/core/api";
import {
  missingDaemonIdConflict,
  parseComputerDeleteConflict,
} from "./delete-computer-conflict";

describe("parseComputerDeleteConflict", () => {
  it("ignores retired Computer delete conflict codes", () => {
    const err = new ApiError("conflict", 409, "Conflict", {
      code: "computer_agent_plan_changed",
      error: "agent set changed",
      active_agents: [{ id: "a1", name: "Agent One" }],
    });
    expect(parseComputerDeleteConflict(err)).toBeNull();
  });

  it("extracts active agents for computer_has_active_agents", () => {
    const err = new ApiError("conflict", 409, "Conflict", {
      code: "computer_has_active_agents",
      active_agents: [{ id: "a1", name: "Agent One" }],
    });
    const parsed = parseComputerDeleteConflict(err);
    expect(parsed?.activeAgents).toHaveLength(1);
    expect(parsed?.activeAgents[0]?.id).toBe("a1");
  });

  it("returns null for non-409", () => {
    expect(
      parseComputerDeleteConflict(new ApiError("nope", 500, "Error", {})),
    ).toBeNull();
  });
});

describe("missingDaemonIdConflict", () => {
  it("is an explicit blocked reason (not silent skip)", () => {
    expect(missingDaemonIdConflict("no daemon").code).toBe("missing_daemon_id");
  });
});
