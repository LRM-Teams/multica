import { describe, expect, it } from "vitest";
import { shouldPinChatToLatest } from "./pin-chat-to-latest";

describe("shouldPinChatToLatest", () => {
  it("pins on the first populated tail", () => {
    expect(shouldPinChatToLatest(undefined, { id: "m1", role: "assistant" })).toBe(true);
  });

  it("pins when the user appends a new message", () => {
    expect(shouldPinChatToLatest("m1", { id: "m2", role: "user" })).toBe(true);
  });

  it("does not pin when only the assistant tail changes", () => {
    expect(shouldPinChatToLatest("m2", { id: "m3", role: "assistant" })).toBe(false);
  });

  it("does not pin when the tail id is unchanged", () => {
    expect(shouldPinChatToLatest("m2", { id: "m2", role: "user" })).toBe(false);
  });
});
