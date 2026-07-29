import { describe, expect, it } from "vitest";
import { ApiError } from "../api";
import { classifyRoleChangeFailure } from "./role-change-failure";

const apiError = (status: number, body?: unknown) =>
  new ApiError("server message we must never render", status, "", body);

describe("classifyRoleChangeFailure (#832 / #847)", () => {
  it("separates owner_changed from a plain 403 — the server keeps them apart so we must too", () => {
    // The whole point of the code: "someone else took ownership" tells the user
    // to refresh; "you may not do this" tells them they never could. Collapsing
    // them shows one of those two a sentence that is false for them.
    expect(classifyRoleChangeFailure(apiError(403, { code: "owner_changed" }))).toBe(
      "owner_changed",
    );
    expect(classifyRoleChangeFailure(apiError(403, { code: "something_else" }))).toBe("forbidden");
    expect(classifyRoleChangeFailure(apiError(403))).toBe("forbidden");
    expect(classifyRoleChangeFailure(apiError(403, {}))).toBe("forbidden");
  });

  it("does not mistake a non-string code for the marker", () => {
    expect(classifyRoleChangeFailure(apiError(403, { code: 1 }))).toBe("forbidden");
    expect(classifyRoleChangeFailure(apiError(403, { code: null }))).toBe("forbidden");
  });

  it.each([
    [409, "conflict"],
    [404, "gone"],
    [400, "contract"],
    [500, "transient"],
    [502, "transient"],
  ] as const)("maps status %i to %s", (status, expected) => {
    expect(classifyRoleChangeFailure(apiError(status))).toBe(expected);
  });

  it("never keys on the server message", () => {
    // Same message, different statuses → different kinds. If anything ever
    // pattern-matched the message, these would collapse together.
    const sameMessage = "server message we must never render";
    expect(new ApiError(sameMessage, 409, "").message).toBe(sameMessage);
    expect(classifyRoleChangeFailure(apiError(409))).not.toBe(
      classifyRoleChangeFailure(apiError(404)),
    );
  });

  it("treats non-ApiError as transient — a network/parse failure is retryable, not a verdict", () => {
    expect(classifyRoleChangeFailure(new Error("network down"))).toBe("transient");
    expect(classifyRoleChangeFailure(undefined)).toBe("transient");
    expect(classifyRoleChangeFailure("boom")).toBe("transient");
  });
});
