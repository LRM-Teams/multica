import { beforeEach, describe, expect, it, vi } from "vitest";

const { mockPatch, mockSetUser, mockSetPerson } = vi.hoisted(() => ({
  mockPatch: vi.fn(),
  mockSetUser: vi.fn(),
  mockSetPerson: vi.fn(),
}));

vi.mock("../api", () => ({ api: { patchOnboarding: mockPatch } }));
vi.mock("../auth", () => ({
  useAuthStore: { getState: () => ({ setUser: mockSetUser }) },
}));
vi.mock("../analytics", () => ({ setPersonProperties: mockSetPerson }));

import { saveQuestionnaire } from "./store";

beforeEach(() => {
  mockPatch.mockReset().mockResolvedValue({ id: "u1" });
  mockSetUser.mockReset();
  mockSetPerson.mockReset();
});

describe("saveQuestionnaire", () => {
  it("persists via a single PATCH carrying the questionnaire", async () => {
    await saveQuestionnaire({ source: ["search"] });
    expect(mockPatch).toHaveBeenCalledWith({
      questionnaire: { source: ["search"] },
    });
  });

  it("resolves once the PATCH persists even if the store sync throws (task #230)", async () => {
    // The reported bug: a 2xx PATCH persisted the source, but a throw in the
    // best-effort post-persist tail meant the caller's `.then` (which
    // dismisses the source-backfill modal) never ran — trapping the user
    // behind the modal despite their answer being saved.
    mockSetUser.mockImplementation(() => {
      throw new Error("store hiccup");
    });
    await expect(
      saveQuestionnaire({ source: ["search"] }),
    ).resolves.toBeUndefined();
    expect(mockPatch).toHaveBeenCalledTimes(1);
  });

  it("resolves even if analytics mirroring throws", async () => {
    mockSetPerson.mockImplementation(() => {
      throw new Error("posthog down");
    });
    await expect(
      saveQuestionnaire({ source: ["search"] }),
    ).resolves.toBeUndefined();
  });

  it("rejects when the PATCH itself fails (persistence not achieved)", async () => {
    mockPatch.mockRejectedValue(new Error("500"));
    await expect(saveQuestionnaire({ source: ["search"] })).rejects.toThrow(
      "500",
    );
    expect(mockSetUser).not.toHaveBeenCalled();
  });
});
