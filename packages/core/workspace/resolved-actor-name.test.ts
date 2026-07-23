import { describe, expect, it } from "vitest";
import {
  DIRECTORY_MISS_SENTINELS,
  directoryActorDisplayName,
  isDirectoryActorMiss,
  profileActorDisplayName,
  toDirectoryActorType,
  toMemberProfileType,
} from "./resolved-actor-name";

describe("resolved-actor-name (LRM-364 / LRM-281)", () => {
  it("treats Unknown Agent / Unknown / empty as directory misses", () => {
    expect(isDirectoryActorMiss("Unknown Agent")).toBe(true);
    expect(isDirectoryActorMiss("Unknown")).toBe(true);
    expect(isDirectoryActorMiss("  ")).toBe(true);
    expect(isDirectoryActorMiss("贝克汉姆")).toBe(false);
    expect(DIRECTORY_MISS_SENTINELS.has("Unknown Agent")).toBe(true);
  });

  it("reads directory hits without inventing a fallback name", () => {
    const getActorName = (type: string, id: string, fallback?: string) => {
      if (type === "agent" && id === "agent-1") return "Research Agent";
      return fallback ?? "Unknown Agent";
    };
    expect(directoryActorDisplayName(getActorName, "agent", "agent-1")).toBe("Research Agent");
    expect(directoryActorDisplayName(getActorName, "agent", "agent-beckham")).toBeNull();
  });

  it("reads profile display_name then name", () => {
    expect(
      profileActorDisplayName({ display_name: "贝克汉姆", name: "bei-ke-han-mu" }),
    ).toBe("贝克汉姆");
    expect(profileActorDisplayName({ display_name: "", name: "bei-ke-han-mu" })).toBe(
      "bei-ke-han-mu",
    );
    expect(profileActorDisplayName({ display_name: "", name: "" })).toBeNull();
    expect(profileActorDisplayName(undefined)).toBeNull();
  });

  it("maps reaction/system actor types onto directory + profile API types", () => {
    expect(toDirectoryActorType("agent")).toBe("agent");
    expect(toDirectoryActorType("member")).toBe("member");
    expect(toDirectoryActorType("human")).toBe("member");
    expect(toDirectoryActorType("user")).toBe("member");
    expect(toDirectoryActorType("system")).toBeNull();
    expect(toMemberProfileType("agent")).toBe("agent");
    expect(toMemberProfileType("member")).toBe("user");
  });
});
