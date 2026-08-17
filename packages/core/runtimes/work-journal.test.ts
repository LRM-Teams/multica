import { describe, expect, it } from "vitest";
import type { ComputerConnection } from "../types";
import { localMachineWorkUncollected } from "./work-journal";

function computer(overrides: Partial<ComputerConnection> = {}): ComputerConnection {
  return {
    daemon_id: "computer-1",
    owner_id: "user-1",
    connected: true,
    last_seen_at: "2026-08-17T00:00:00Z",
    ...overrides,
  };
}

describe("localMachineWorkUncollected", () => {
  it("is true when the viewer has no enabled Computer", () => {
    expect(localMachineWorkUncollected(undefined, "user-1")).toBe(true);
    expect(localMachineWorkUncollected([], "user-1")).toBe(true);
    expect(localMachineWorkUncollected([computer()], "user-1")).toBe(true);
    expect(localMachineWorkUncollected(
      [computer({ work_journal_enabled: false })],
      "user-1",
    )).toBe(true);
    expect(localMachineWorkUncollected(
      [computer({ owner_id: "other", work_journal_enabled: true })],
      "user-1",
    )).toBe(true);
  });

  it("is false when at least one owned Computer has Journal on", () => {
    expect(localMachineWorkUncollected(
      [
        computer({ daemon_id: "off", work_journal_enabled: false }),
        computer({ daemon_id: "on", work_journal_enabled: true }),
      ],
      "user-1",
    )).toBe(false);
  });
});
